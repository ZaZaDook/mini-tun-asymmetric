// Package dns implements the master's in-tunnel DNS forwarder.
//
// Clients are forced (by the client app) to send all DNS to the tunnel gateway
// (e.g. 10.8.0.1:53). The master answers on that address and forwards queries
// upstream over one of three transports — plain UDP, DNS-over-TLS, or
// DNS-over-HTTPS — returning real A records so a router's fake-dns never gets a
// chance to inject fake 198.18.x.x addresses.
//
// It operates on raw DNS wire-format messages (RFC 1035): queries are forwarded
// verbatim and responses returned verbatim. Only the minimal header/question
// fields needed for caching and optional AAAA suppression are parsed.
package dns

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Mode selects the upstream transport.
type Mode string

const (
	ModePlain Mode = "plain"
	ModeDoT   Mode = "dot"
	ModeDoH   Mode = "doh"
)

const (
	qtypeA    = 1
	qtypeAAAA = 28
	maxMsg    = 4096
)

var errBadMsg = errors.New("malformed DNS message")

// Resolver forwards DNS queries upstream over the configured transport.
type Resolver struct {
	mode         Mode
	upstream     string // host:port for plain/dot
	dohURL       string // endpoint for doh
	suppressAAAA bool
	logf         func(string, ...any)

	httpc *http.Client // for DoH
	cache *cache
}

// Config configures a Resolver.
type Config struct {
	Mode         Mode
	Upstream     string // e.g. "8.8.8.8:53" (plain) or "1.1.1.1:853" (dot)
	DoHURL       string // e.g. "https://cloudflare-dns.com/dns-query"
	SuppressAAAA bool
	Logf         func(string, ...any)
}

// New builds a Resolver, applying defaults for empty fields.
// By default AAAA (IPv6) answers are passed through — clients may receive real
// IPv6 addresses. Set SuppressAAAA to true to drop AAAA when the tunnel is
// IPv4-only.
func New(cfg Config) (*Resolver, error) {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	switch cfg.Mode {
	case ModePlain, ModeDoT, ModeDoH:
	case "":
		cfg.Mode = ModePlain
	default:
		return nil, fmt.Errorf("unknown dns mode %q", cfg.Mode)
	}
	if cfg.Mode == ModeDoH && cfg.DoHURL == "" {
		return nil, errors.New("doh mode requires dns_upstream_doh URL")
	}
	if (cfg.Mode == ModePlain || cfg.Mode == ModeDoT) && cfg.Upstream == "" {
		return nil, errors.New("plain/dot mode requires dns_upstream host:port")
	}
	r := &Resolver{
		mode:         cfg.Mode,
		upstream:     cfg.Upstream,
		dohURL:       cfg.DoHURL,
		suppressAAAA: cfg.SuppressAAAA,
		logf:         cfg.Logf,
		cache:        newCache(),
	}
	if cfg.Mode == ModeDoH {
		r.httpc = &http.Client{Timeout: 8 * time.Second}
	}
	return r, nil
}

// Query takes a raw DNS query (wire format) and returns a raw DNS response.
// It is safe for concurrent use.
func (r *Resolver) Query(query []byte) ([]byte, error) {
	if len(query) < 12 {
		return nil, errBadMsg
	}
	name, qtype, ok := parseQuestion(query)
	if !ok {
		// Forward anything we can't parse rather than dropping it.
		return r.forward(query)
	}

	// Suppress AAAA: answer immediately with an empty NOERROR response so the
	// client falls back to IPv4 (data path is IPv4-only; an AAAA route can't be
	// tunneled and would leak to the router).
	if r.suppressAAAA && qtype == qtypeAAAA {
		return emptyAnswer(query), nil
	}

	key := cacheKey(name, qtype)
	if resp, ok := r.cache.get(key); ok {
		// Rewrite the cached response's transaction ID to match this query.
		out := append([]byte(nil), resp...)
		copy(out[0:2], query[0:2])
		return out, nil
	}

	resp, err := r.forward(query)
	if err != nil {
		return nil, err
	}
	if ttl := minTTL(resp); ttl > 0 {
		r.cache.put(key, resp, time.Duration(ttl)*time.Second)
	}
	return resp, nil
}

// forward sends the query upstream using the configured transport.
func (r *Resolver) forward(query []byte) ([]byte, error) {
	switch r.mode {
	case ModePlain:
		return r.forwardPlain(query)
	case ModeDoT:
		return r.forwardDoT(query)
	case ModeDoH:
		return r.forwardDoH(query)
	}
	return nil, fmt.Errorf("unknown mode %q", r.mode)
}

func (r *Resolver) forwardPlain(query []byte) ([]byte, error) {
	c, err := net.DialTimeout("udp", r.upstream, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, maxMsg)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, err
		}
		// Drop replies whose transaction ID doesn't match the query — a cheap
		// guard against an off-path spoofed answer racing the real one.
		if n >= 2 && buf[0] == query[0] && buf[1] == query[1] {
			return append([]byte(nil), buf[:n]...), nil
		}
	}
}

// forwardDoT sends the query over DNS-over-TLS (RFC 7858): a 2-byte length
// prefix followed by the message, over a TLS connection to host:853.
func (r *Resolver) forwardDoT(query []byte) ([]byte, error) {
	host, _, err := net.SplitHostPort(r.upstream)
	if err != nil {
		host = r.upstream
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", r.upstream, &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(6 * time.Second))
	if err := writeTCPMsg(conn, query); err != nil {
		return nil, err
	}
	return readTCPMsg(conn)
}

// forwardDoH sends the query over DNS-over-HTTPS (RFC 8484) as a POST with
// Content-Type application/dns-message.
func (r *Resolver) forwardDoH(query []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, r.dohURL, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := r.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMsg))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func writeTCPMsg(w io.Writer, msg []byte) error {
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(msg)))
	if _, err := w.Write(l[:]); err != nil {
		return err
	}
	_, err := w.Write(msg)
	return err
}

func readTCPMsg(r io.Reader) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(l[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// parseQuestion extracts the first question's name (lowercased) and qtype.
func parseQuestion(msg []byte) (name string, qtype uint16, ok bool) {
	if len(msg) < 12 {
		return "", 0, false
	}
	qd := binary.BigEndian.Uint16(msg[4:6])
	if qd < 1 {
		return "", 0, false
	}
	off := 12
	var sb strings.Builder
	for {
		if off >= len(msg) {
			return "", 0, false
		}
		l := int(msg[off])
		off++
		if l == 0 {
			break
		}
		if l&0xC0 != 0 { // compression pointer not expected in a question
			return "", 0, false
		}
		if off+l > len(msg) {
			return "", 0, false
		}
		sb.Write(bytes.ToLower(msg[off : off+l]))
		sb.WriteByte('.')
		off += l
	}
	if off+4 > len(msg) {
		return "", 0, false
	}
	qtype = binary.BigEndian.Uint16(msg[off : off+2])
	return sb.String(), qtype, true
}

// emptyAnswer builds a NOERROR response with zero answers for the given query,
// echoing only its question section. Used to suppress AAAA. The message is
// truncated to the end of the first question so any EDNS0/OPT record in the
// original additional section is dropped — otherwise the response would carry
// trailing bytes while claiming ARCOUNT=0 (a malformed message some clients
// reject).
func emptyAnswer(query []byte) []byte {
	// Find the end of the first question: QNAME then QTYPE(2)+QCLASS(2).
	end := 12
	for end < len(query) {
		l := int(query[end])
		if l == 0 {
			end++
			break
		}
		if l&0xC0 != 0 { // compression pointer (not expected in a question)
			end += 2
			break
		}
		end += 1 + l
	}
	end += 4 // QTYPE + QCLASS
	if end > len(query) {
		end = len(query)
	}
	out := append([]byte(nil), query[:end]...)
	out[2] |= 0x80 // QR (response)
	out[3] |= 0x80 // RA (recursion available); RD is preserved from the query
	binary.BigEndian.PutUint16(out[4:6], 1)   // QDCOUNT = 1
	binary.BigEndian.PutUint16(out[6:8], 0)   // ANCOUNT = 0
	binary.BigEndian.PutUint16(out[8:10], 0)  // NSCOUNT = 0
	binary.BigEndian.PutUint16(out[10:12], 0) // ARCOUNT = 0
	return out
}

// minTTL scans answer RRs and returns the smallest TTL (seconds), or 0 if none.
func minTTL(msg []byte) uint32 {
	if len(msg) < 12 {
		return 0
	}
	an := binary.BigEndian.Uint16(msg[6:8])
	if an == 0 {
		return 0
	}
	// Skip header + question section.
	qd := binary.BigEndian.Uint16(msg[4:6])
	off := 12
	for i := 0; i < int(qd); i++ {
		o, ok := skipName(msg, off)
		if !ok || o+4 > len(msg) {
			return 0
		}
		off = o + 4
	}
	var min uint32
	for i := 0; i < int(an); i++ {
		o, ok := skipName(msg, off)
		if !ok || o+10 > len(msg) {
			break
		}
		ttl := binary.BigEndian.Uint32(msg[o+4 : o+8])
		rdlen := int(binary.BigEndian.Uint16(msg[o+8 : o+10]))
		off = o + 10 + rdlen
		if i == 0 || ttl < min {
			min = ttl
		}
	}
	return min
}

// skipName advances past a (possibly compressed) DNS name, returning the offset
// just after it.
func skipName(msg []byte, off int) (int, bool) {
	for {
		if off >= len(msg) {
			return 0, false
		}
		l := int(msg[off])
		if l == 0 {
			return off + 1, true
		}
		if l&0xC0 == 0xC0 { // compression pointer is 2 bytes, terminates the name
			return off + 2, true
		}
		off += 1 + l
	}
}

func cacheKey(name string, qtype uint16) string {
	return name + "|" + fmt.Sprint(qtype)
}

// --- tiny TTL cache ---------------------------------------------------------

type cacheEntry struct {
	resp    []byte
	expires time.Time
}

type cache struct {
	mu sync.RWMutex
	m  map[string]cacheEntry
}

func newCache() *cache { return &cache{m: make(map[string]cacheEntry)} }

func (c *cache) get(key string) ([]byte, bool) {
	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.resp, true
}

func (c *cache) put(key string, resp []byte, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	if ttl > 1*time.Hour {
		ttl = 1 * time.Hour
	}
	c.mu.Lock()
	if len(c.m) > 4096 { // crude cap; flush on overflow
		c.m = make(map[string]cacheEntry)
	}
	c.m[key] = cacheEntry{resp: append([]byte(nil), resp...), expires: time.Now().Add(ttl)}
	c.mu.Unlock()
}
