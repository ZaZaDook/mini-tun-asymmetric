#!/bin/bash
# fakedns-test.sh — prove the VPN defeats router-style fake-dns.
#
# Emulates a passwall2 router: a fake-dns inside the namespace that answers
# EVERY A query with a fake 198.18.x.x address. Then compares:
#   BEFORE: no VPN  -> curl by name resolves to fake IP -> fails
#   AFTER:  VPN on  -> client forces DNS to in-tunnel resolver -> real IP -> works
#
# Run on the slave host. Assumes netns-test.sh already set up "vpntest".
set -u
NS=vpntest
TOKEN="${MINI_TUN_ASYMMETRIC_TOKEN:-$(cat ~/.mini-tun-asymmetric_token 2>/dev/null)}"
MASTER=203.0.113.10:7000
FAKE_IP=198.18.0.99

# --- fake-dns server: answers all A queries with FAKE_IP ---
cat > /tmp/fakedns.py <<PY
import socket,struct,sys
FAKE=bytes(int(x) for x in "${FAKE_IP}".split("."))
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
s.bind(("127.0.0.1",53))
sys.stderr.write("fakedns up on 127.0.0.1:53\n"); sys.stderr.flush()
while True:
    try:
        data,addr=s.recvfrom(2048)
        tid=data[0:2]; q=data[12:]
        # build response: echo question, 1 answer = FAKE_IP, TTL 60
        resp=tid+b"\x81\x80"+b"\x00\x01\x00\x01\x00\x00\x00\x00"+q
        resp+=b"\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x3c\x00\x04"+FAKE
        s.sendto(resp,addr)
    except Exception as e:
        sys.stderr.write(f"err {e}\n")
PY

echo "############ FAKE-DNS DEFEAT TEST ############"
# Point the namespace's resolver at the fake-dns (like a passwall2 router would).
echo "nameserver 127.0.0.1" > /etc/netns/$NS/resolv.conf
ip netns exec $NS python3 /tmp/fakedns.py 2>/tmp/fakedns.log &
FDPID=$!
sleep 1

echo "=== BASELINE (fake-dns active, NO VPN) ==="
echo -n "resolv.conf in ns: "; ip netns exec $NS cat /etc/resolv.conf | grep nameserver
echo -n "getent ip.sb -> "; ip netns exec $NS getent hosts ip.sb | head -1 || echo "(none)"
echo -n "curl https://ip.sb (by name): "
ip netns exec $NS curl -s -m 10 -o /dev/null -w "HTTP %{http_code}\n" https://ip.sb 2>&1 || echo "FAILED (expected — fake IP)"

echo
echo "=== WITH VPN (full-tunnel + secure-dns) ==="
ip netns exec $NS /tmp/mini-tun-asymmetric-cli -master $MASTER -token $TOKEN -full -secure-dns -wait 30s >/tmp/cli_full.log 2>&1 &
CLIPID=$!
sleep 8
echo -n "resolv.conf in ns now: "; ip netns exec $NS cat /etc/resolv.conf | grep nameserver
echo -n "curl https://ip.sb (by name): "
ip netns exec $NS curl -s -m 15 https://ip.sb 2>&1 | head -1 || echo "FAILED"
echo -n "exit IP via trace (expect master 203.0.113.10): "
ip netns exec $NS curl -s -m 15 https://1.1.1.1/cdn-cgi/trace 2>&1 | grep -E "^ip=" || echo "(trace failed)"
echo "--- client log (no dbg) ---"
grep -iE "connected|DNS|IPv6|state" /tmp/cli_full.log | head -10

# cleanup
kill $CLIPID 2>/dev/null; kill $FDPID 2>/dev/null
sleep 1
echo "=== done ==="
