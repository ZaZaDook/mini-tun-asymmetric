#!/bin/bash
# wsl-fakedns-test.sh — real-router fake-dns defeat test, run entirely in ONE
# WSL invocation (the distro idle-terminates between separate wsl.exe calls and
# would wipe the namespace otherwise).
#
# Baseline uses the WSL DNS proxy 10.255.255.254, which forwards to the user's
# real passwall2 router and returns fake 198.18.x.x — reproducing the bug for real.
set -u
TOKEN="${MINI_TUN_ASYMMETRIC_TOKEN:-$(cat ~/.mini-tun-asymmetric_token 2>/dev/null)}"
MASTER=203.0.113.10:7000
ROUTER_DNS=10.255.255.254

/root/netns-test.sh teardown >/dev/null 2>&1
/root/netns-test.sh setup >/dev/null 2>&1

# Point the namespace's baseline resolver at the real router (via WSL proxy).
echo "nameserver ${ROUTER_DNS}" > /etc/netns/vpntest/resolv.conf

echo "############ BASELINE (real router fake-dns, NO VPN) ############"
echo -n "ns resolver: "; ip netns exec vpntest cat /etc/resolv.conf | grep nameserver
echo -n "getent ip.sb -> "; ip netns exec vpntest getent hosts ip.sb 2>&1 | head -1 || echo "(fail)"
echo -n "curl https://ip.sb -> "; ip netns exec vpntest curl -s -m 8 -o /dev/null -w "HTTP %{http_code}\n" https://ip.sb 2>&1 || echo "FAILED"

echo
echo "############ WITH VPN (full-tunnel + secure-dns) ############"
ip netns exec vpntest /root/mini-tun-asymmetric-cli -master $MASTER -token $TOKEN -full -secure-dns -wait 35s >/tmp/cli_wsl.log 2>&1 &
sleep 9
echo -n "ns resolver now: "; ip netns exec vpntest cat /etc/resolv.conf | grep nameserver
echo -n "in-tunnel DNS ip.sb -> "; ip netns exec vpntest python3 /root/reg.py ip.sb 2>&1
echo -n "curl https://ip.sb (by name) -> "; ip netns exec vpntest curl -s -m 15 https://ip.sb 2>&1 | head -1 || echo "FAILED"
echo -n "exit IP (expect master 203.0.113.10) -> "; ip netns exec vpntest curl -s -m 15 https://1.1.1.1/cdn-cgi/trace 2>&1 | grep "^ip=" || echo "(fail)"
echo "--- client log ---"; grep -iE "connected|DNS|state" /tmp/cli_wsl.log | head -8

/root/netns-test.sh teardown >/dev/null 2>&1
echo "=== done ==="
