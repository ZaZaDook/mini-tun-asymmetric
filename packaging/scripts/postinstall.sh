#!/bin/sh
# Runs after install/upgrade. Both units ship disabled; the role is chosen with
# mta-setup, which writes the config and enables the right service. We only
# reload systemd here (starting without a config would just crash-loop).
set -e

systemctl daemon-reload 2>/dev/null || true

cat <<'EOF'

  Mini-Tun Asymmetric installed.

  Next step — choose this server's role and generate its config:

      sudo mta-setup

  Pick "Quick Setup Wizard" → Master or Slave. The wizard generates the
  auth token (master) or takes it (slave), writes the config, opens the
  firewall, and starts the service.

EOF
