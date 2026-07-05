#!/bin/sh
# Runs before removal. Stop and disable both roles' services so nothing lingers.
# On upgrade the package manager reinstalls the unit files right after; a running
# service is restarted by postinstall's daemon-reload + the admin, not here.
set -e

for svc in mini-tun-asymmetric-master mini-tun-asymmetric-slave; do
    systemctl stop "$svc" 2>/dev/null || true
    systemctl disable "$svc" 2>/dev/null || true
done
