#!/bin/sh
# Runs after removal. Reload systemd so the removed units drop out. The config
# directory /etc/mini-tun-asymmetric (with the auth token and TLS) is left in
# place on purpose so an upgrade or reinstall keeps working; remove it by hand
# for a full purge.
set -e

systemctl daemon-reload 2>/dev/null || true
