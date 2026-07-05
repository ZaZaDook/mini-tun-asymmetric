Mini-Tun Asymmetric — server node (master + slave)
==================================================

This package installs both server roles. Pick one per host after install:

    sudo mta-setup

Choose "Quick Setup Wizard", then Master or Slave.

  Master  — the entry point + internet egress. The wizard generates an auth
            token (copy it to the slaves and clients), a self-signed TLS cert,
            writes /etc/mini-tun-asymmetric/master.json, opens the firewall, and
            starts mini-tun-asymmetric-master.

  Slave   — relays downlink to clients. The wizard asks for the master host and
            the same auth token, writes /etc/mini-tun-asymmetric/slave.json,
            opens the firewall, and starts mini-tun-asymmetric-slave.

Default ports
  master: UDP 7000 (clients), TCP 7001 (control), UDP 7003 (data plane),
          plus the ephemeral range 32768-60999/udp for per-session data ports.
  slave:  UDP 7002 (downlink to clients), UDP 7004 (data plane from master).

Files
  /usr/bin/mini-tun-asymmetric-master
  /usr/bin/mini-tun-asymmetric-slave
  /usr/bin/mta-setup
  /etc/mini-tun-asymmetric/            config + TLS (created by the wizard)
  /usr/lib/systemd/system/mini-tun-asymmetric-{master,slave}.service

Management: re-run `mta-setup` any time to edit config, start/stop, or view logs.

This is early-alpha, unaudited software. See the project README for details.
