#!/bin/bash
set -e

# Auth token shared by master, slave, and clients. Generate a fresh one with:
#   openssl rand -base64 32
# Override at run time:  TOKEN="..." SLAVE_IP="..." ./setup-master.sh
TOKEN="${TOKEN:-REPLACE_WITH_BASE64_TOKEN}"
SLAVE_IP="${SLAVE_IP:-REPLACE_WITH_SLAVE_IP}"

echo "[1/6] Installing binary..."
# already installed, just verify
ls -la /usr/local/bin/mini-tun-asymmetric-master

echo "[2/6] Generating TLS certs..."
mkdir -p /etc/mini-tun-asymmetric/tls
openssl req -x509 -newkey rsa:4096 \
  -keyout /etc/mini-tun-asymmetric/tls/privkey.pem \
  -out /etc/mini-tun-asymmetric/tls/fullchain.pem \
  -days 3650 -nodes \
  -subj "/CN=mini-tun-asymmetric-master"
echo "TLS generated"

echo "[3/6] Writing master config..."
cat > /etc/mini-tun-asymmetric/master.json << EOF
{
  "listen_udp": "0.0.0.0:7000",
  "listen_control": "0.0.0.0:7001",
  "listen_data_plane": "0.0.0.0:7003",
  "tunnel_subnet": "10.8.0.0/24",
  "tunnel_ip": "10.8.0.1",
  "auth_token": "${TOKEN}",
  "server_id": "master01",
  "slaves": [{"id":"slave01","address":"${SLAVE_IP}:7002"}],
  "tls_cert_file": "/etc/mini-tun-asymmetric/tls/fullchain.pem",
  "tls_key_file": "/etc/mini-tun-asymmetric/tls/privkey.pem",
  "dns_upstream": "8.8.8.8:53",
  "log_level": "info"
}
EOF
echo "Config written"
cat /etc/mini-tun-asymmetric/master.json

echo "[4/6] Creating systemd service..."
cat > /etc/systemd/system/mini-tun-asymmetric-master.service << 'SVCEOF'
[Unit]
Description=Mini-Tun Asymmetric Master Node
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/mini-tun-asymmetric-master -config /etc/mini-tun-asymmetric/master.json
Restart=always
RestartSec=5
LimitNOFILE=65536
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
SVCEOF

echo "[5/6] Opening firewall ports..."
firewall-cmd --permanent --add-port=7000/udp 2>/dev/null || ufw allow 7000/udp 2>/dev/null || true
firewall-cmd --permanent --add-port=7001/tcp 2>/dev/null || ufw allow 7001/tcp 2>/dev/null || true
firewall-cmd --permanent --add-port=7003/udp 2>/dev/null || ufw allow 7003/udp 2>/dev/null || true
firewall-cmd --reload 2>/dev/null || true
iptables -I INPUT -p udp --dport 7000 -j ACCEPT 2>/dev/null || true
iptables -I INPUT -p tcp --dport 7001 -j ACCEPT 2>/dev/null || true
iptables -I INPUT -p udp --dport 7003 -j ACCEPT 2>/dev/null || true
echo "Firewall done"

echo "[6/6] Starting service..."
systemctl daemon-reload
systemctl enable mini-tun-asymmetric-master
systemctl start mini-tun-asymmetric-master
sleep 2
systemctl status mini-tun-asymmetric-master --no-pager

echo ""
echo "=== MASTER SETUP COMPLETE ==="
echo "Token configured (keep it secret; share the same value with slaves/clients)."
echo "Control endpoint: <this-server-ip>:7001"
