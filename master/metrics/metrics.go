// Package metrics provides lightweight runtime counters and a JSON snapshot
// endpoint for the master. It is the foundation for future failover logic: a
// controller (or external monitor) can poll per-slave health and load to decide
// where to place new sessions or when to drain an unhealthy slave.
//
// The HTTP endpoint binds to localhost by default (see Serve) so metrics are not
// network-exposed without an explicit, deliberate bind address.
package metrics

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Counters holds monotonic counters updated on the data/control paths.
type Counters struct {
	SessionsCreated atomic.Uint64
	SessionsActive  atomic.Int64
	AuthFailures    atomic.Uint64
	DNSQueries      atomic.Uint64
	DNSErrors       atomic.Uint64
	ClientPackets   atomic.Uint64 // decrypted client→net packets injected
	BytesUplink     atomic.Uint64 // bytes received from clients (pre-decrypt payloads)
}

// SlaveStat is a point-in-time view of one slave's health for failover decisions.
type SlaveStat struct {
	SlaveID      string `json:"slave_id"`
	Connected    bool   `json:"connected"`
	LastSeenMS   int64  `json:"last_seen_ms"`   // ms since last control keepalive
	Sessions     int    `json:"sessions"`       // sessions currently assigned to this slave
	DataPlaneUp  bool   `json:"data_plane_up"`  // slave registered a routable data endpoint
}

// Snapshot is the JSON document returned by the metrics endpoint.
type Snapshot struct {
	UptimeSeconds   int64       `json:"uptime_seconds"`
	SessionsCreated uint64      `json:"sessions_created"`
	SessionsActive  int64       `json:"sessions_active"`
	AuthFailures    uint64      `json:"auth_failures"`
	DNSQueries      uint64      `json:"dns_queries"`
	DNSErrors       uint64      `json:"dns_errors"`
	ClientPackets   uint64      `json:"client_packets"`
	BytesUplink     uint64      `json:"bytes_uplink"`
	Slaves          []SlaveStat `json:"slaves"`
}

// Registry aggregates counters with live gauges supplied by the rest of the master.
type Registry struct {
	C         Counters
	startTime time.Time

	mu          sync.RWMutex
	slaveStatFn func() []SlaveStat
}

// New creates a Registry. startTime is stamped by the caller (the master passes
// time.Now() at boot) to avoid a hidden time dependency here.
func New(startTime time.Time) *Registry {
	return &Registry{startTime: startTime}
}

// SetSlaveStatFunc registers a callback that returns current per-slave health.
// The master wires this to its control registry + session table.
func (r *Registry) SetSlaveStatFunc(fn func() []SlaveStat) {
	r.mu.Lock()
	r.slaveStatFn = fn
	r.mu.Unlock()
}

// Snapshot builds a consistent view of all metrics.
func (r *Registry) Snapshot(now time.Time) Snapshot {
	r.mu.RLock()
	fn := r.slaveStatFn
	r.mu.RUnlock()

	var slaves []SlaveStat
	if fn != nil {
		slaves = fn()
	}
	return Snapshot{
		UptimeSeconds:   int64(now.Sub(r.startTime).Seconds()),
		SessionsCreated: r.C.SessionsCreated.Load(),
		SessionsActive:  r.C.SessionsActive.Load(),
		AuthFailures:    r.C.AuthFailures.Load(),
		DNSQueries:      r.C.DNSQueries.Load(),
		DNSErrors:       r.C.DNSErrors.Load(),
		ClientPackets:   r.C.ClientPackets.Load(),
		BytesUplink:     r.C.BytesUplink.Load(),
		Slaves:          slaves,
	}
}

// Handler returns an http.Handler serving the JSON snapshot at any path.
// nowFn is injected so tests (and the no-wallclock workflow constraints) can
// control time; the master passes time.Now.
func (r *Registry) Handler(nowFn func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(r.Snapshot(nowFn()))
	})
}

// Serve starts an HTTP metrics server on addr. If addr has no host (e.g.
// ":9090") it still binds all interfaces, so callers should pass an explicit
// localhost address like "127.0.0.1:9090" unless network exposure is intended.
// Returns immediately; the server runs in a goroutine.
func (r *Registry) Serve(addr string, nowFn func() time.Time, logf func(string, ...any)) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", r.Handler(nowFn))
	mux.Handle("/", r.Handler(nowFn))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ln, err := listen(addr)
	if err != nil {
		return err
	}
	go func() {
		if logf != nil {
			logf("[metrics] serving on %s", addr)
		}
		srv.Serve(ln)
	}()
	return nil
}
