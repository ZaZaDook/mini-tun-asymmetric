#!/bin/bash
# netns-test.sh — isolated VPN client test harness.
#
# Creates a network namespace "vpntest" with its own veth + NAT egress, so a VPN
# client run inside it cannot touch host networking (SSH stays safe). The /1
# full-tunnel routes the client installs live only inside this namespace.
#
# Usage:
#   netns-test.sh setup      # create namespace + NAT + DNS
#   netns-test.sh exec CMD   # run CMD inside the namespace
#   netns-test.sh teardown   # remove everything
set -u

NS=vpntest
HOST_VETH=veth-h
NS_VETH=veth-n
HOST_IP=10.200.0.1
NS_IP=10.200.0.2
SUBNET=10.200.0.0/24
MASTER_IP=203.0.113.10
SLAVE_IP=203.0.113.20

# Detect the host's main egress interface (the one with the default route).
WAN_IF=$(ip route show default | awk '/default/{print $5; exit}')

case "${1:-}" in
setup)
  ip netns add $NS 2>/dev/null
  ip link add $HOST_VETH type veth peer name $NS_VETH 2>/dev/null
  ip link set $NS_VETH netns $NS
  ip addr add ${HOST_IP}/24 dev $HOST_VETH 2>/dev/null
  ip link set $HOST_VETH up
  ip netns exec $NS ip addr add ${NS_IP}/24 dev $NS_VETH 2>/dev/null
  ip netns exec $NS ip link set $NS_VETH up
  ip netns exec $NS ip link set lo up
  ip netns exec $NS ip route add default via $HOST_IP
  # NAT the namespace's traffic out the host WAN interface.
  sysctl -wq net.ipv4.ip_forward=1
  iptables -t nat -C POSTROUTING -s $SUBNET -o "$WAN_IF" -j MASQUERADE 2>/dev/null \
    || iptables -t nat -A POSTROUTING -s $SUBNET -o "$WAN_IF" -j MASQUERADE
  iptables -C FORWARD -i $HOST_VETH -j ACCEPT 2>/dev/null \
    || iptables -I FORWARD -i $HOST_VETH -j ACCEPT
  iptables -C FORWARD -o $HOST_VETH -j ACCEPT 2>/dev/null \
    || iptables -I FORWARD -o $HOST_VETH -j ACCEPT
  # DNS for the namespace.
  mkdir -p /etc/netns/$NS
  echo "nameserver 8.8.8.8" > /etc/netns/$NS/resolv.conf
  echo "[setup] netns '$NS' ready (WAN_IF=$WAN_IF, server exclusions for $MASTER_IP/$SLAVE_IP added inside ns)"
  # Make sure VPN server traffic always uses the veth path, never a tunnel route.
  ip netns exec $NS ip route add ${MASTER_IP}/32 via $HOST_IP 2>/dev/null
  ip netns exec $NS ip route add ${SLAVE_IP}/32 via $HOST_IP 2>/dev/null
  ;;
exec)
  shift
  exec ip netns exec $NS "$@"
  ;;
teardown)
  ip netns del $NS 2>/dev/null
  ip link del $HOST_VETH 2>/dev/null
  iptables -t nat -D POSTROUTING -s $SUBNET -o "$WAN_IF" -j MASQUERADE 2>/dev/null
  iptables -D FORWARD -i $HOST_VETH -j ACCEPT 2>/dev/null
  iptables -D FORWARD -o $HOST_VETH -j ACCEPT 2>/dev/null
  rm -rf /etc/netns/$NS
  echo "[teardown] netns '$NS' removed"
  ;;
*)
  echo "usage: $0 {setup|exec CMD...|teardown}"; exit 2;;
esac
