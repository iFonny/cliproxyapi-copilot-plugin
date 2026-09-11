#!/usr/bin/env bash
# Per-boot reconciliation: repair the firewall so Docker networks can egress, then
# bring up the Docker daemon and wait until it is ready. Safe to run repeatedly.
set -euo pipefail

# This VM keeps stale iptables-legacy rules whose FORWARD policy is DROP and which
# only whitelist the default docker0 bridge. Compose-created bridge networks then
# lose all outbound connectivity even though the nft rules Docker writes allow it
# (the kernel evaluates both backends, so the legacy DROP wins). Egress is
# allow-all, so stop the legacy backend from dropping forwarded traffic; the nft
# rules still enforce Docker's own network isolation.
if command -v iptables-legacy >/dev/null 2>&1; then
  sudo iptables-legacy -P FORWARD ACCEPT || true
fi

# Start dockerd if it is not already serving. Clear a stale pid file first so a
# fresh boot (which never has a live daemon) does not refuse to start.
if ! sudo docker info >/dev/null 2>&1; then
  if ! pgrep -x dockerd >/dev/null 2>&1; then
    sudo rm -f /var/run/docker.pid
  fi
  sudo bash -c 'nohup dockerd >/var/log/dockerd.log 2>&1 &'
  for _ in $(seq 1 30); do
    if sudo docker info >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
fi

if sudo docker info >/dev/null 2>&1; then
  echo "Docker daemon is ready."
else
  echo "Docker daemon failed to start; recent log:" >&2
  sudo tail -n 30 /var/log/dockerd.log >&2 || true
  exit 1
fi
