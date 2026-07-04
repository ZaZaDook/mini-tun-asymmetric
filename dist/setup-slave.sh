#!/bin/bash
set -e

# Auth token must match the master's. Generate with: openssl rand -base64 32
# Override at run time:  TOKEN="..." MASTER_IP="..." ./setup-slave.sh
TOKEN="${TOKEN:-REPLACE_WITH_BASE64_TOKEN}"
MASTER_IP="${MASTER_IP:-REPLACE_WITH_MASTER_IP}"

echo "[1/5] Installing binary..."
mv /tmp/mini-tun-asymmetric-slave /usr/local/bin/mini-tun-asymmetric-slave
chmod +x /usr/local/bin/mini-tun-asymmetric-slave
mkdir -p /etc/mini-tun-asymmetric
ls -la /usr/local/bin/mini-tun-asymmetric-slave

echo "[2/5] Writing slave config..."
cat > /etc/mini-tun-asymmetric/slave.json << EOF
{
  "master_control": "${MASTER_IP}:7001",
  "listen_udp": "0.0.0.0:7002",
  "listen_data_plane": "0.0.0.0:7004",
  "auth_token": "${TOKEN}",
  "slave_id": "slave01",
  "tls_ca_cert_file": "",
  "log_level": "info"
}
EOF
echo "Config written"
cat /etc/mini-tun-asymmetric/slave.json

echo "[3/5] Creating systemd service..."
cat > /etc/systemd/system/mini-tun-asymmetric-slave.service << 'SVCEOF'
[Unit]
Description=Mini-Tun Asymmetric Slave Node
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/mini-tun-asymmetric-slave -config /etc/mini-tun-asymmetric/slave.json
Restart=always
RestartSec=5
LimitNOFILE=65536
AmbientCapabilities=CAP_NET_ADMIN

[Install]
WantedBy=multi-user.target
SVCEOF

echo "[4/5] Opening firewall ports..."
firewall-cmd --permanent --add-port=7002/udp 2>/dev/null || ufw allow 7002/udp 2>/dev/null || true
firewall-cmd --permanent --add-port=7004/udp 2>/dev/null || ufw allow 7004/udp 2>/dev/null || true
firewall-cmd --reload 2>/dev/null || true
iptables -I INPUT -p udp --dport 7002 -j ACCEPT 2>/dev/null || true
iptables -I INPUT -p udp --dport 7004 -j ACCEPT 2>/dev/null || true
echo "Firewall done"

echo "[5/5] Starting service..."
systemctl daemon-reload
systemctl enable mini-tun-asymmetric-slave
systemctl start mini-tun-asymmetric-slave
sleep 2
systemctl status mini-tun-asymmetric-slave --no-pager

echo ""
echo "=== SLAVE SETUP COMPLETE ==="
echo "Token configured (must match the master's)."
echo "Master: ${MASTER_IP}:7001"
