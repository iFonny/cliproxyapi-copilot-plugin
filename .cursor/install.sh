#!/usr/bin/env bash
# Idempotent Cloud Agent bootstrap: prepare apt for the egress proxy, install the
# Docker toolchain used by `make build` / `make test-docker` / `make package` and
# the Compose deployment, and warm the Go module and build caches.
set -euo pipefail

# The Cloud egress proxy does not support HTTP pipelining, and the Ubuntu mirrors
# are only reachable over HTTPS. Without these two adjustments apt hangs forever.
sudo tee /etc/apt/apt.conf.d/99cursor-egress.conf >/dev/null <<'EOF'
Acquire::http::Pipeline-Depth "0";
Acquire::https::Pipeline-Depth "0";
Acquire::ForceIPv4 "true";
EOF
if [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
  sudo sed -i \
    -e 's|http://archive.ubuntu.com/ubuntu/|https://archive.ubuntu.com/ubuntu/|g' \
    -e 's|http://security.ubuntu.com/ubuntu/|https://security.ubuntu.com/ubuntu/|g' \
    /etc/apt/sources.list.d/ubuntu.sources
fi

# Docker Engine + Compose v2 (only when missing so re-runs stay fast).
if ! command -v dockerd >/dev/null 2>&1; then
  . /etc/os-release
  sudo install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
    | sudo gpg --batch --yes --dearmor -o /etc/apt/keyrings/docker.gpg
  sudo chmod a+r /etc/apt/keyrings/docker.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" \
    | sudo tee /etc/apt/sources.list.d/docker.list >/dev/null
  sudo apt-get update
  sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
    -o Dpkg::Options::=--force-confold \
    docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin fuse-overlayfs
fi

# fuse-overlayfs is the storage driver that works in this nested-container VM.
sudo mkdir -p /etc/docker
if [ ! -f /etc/docker/daemon.json ]; then
  echo '{ "storage-driver": "fuse-overlayfs" }' | sudo tee /etc/docker/daemon.json >/dev/null
fi

# Let the agent user drive Docker without sudo (group membership applies on next login).
sudo groupadd -f docker
sudo usermod -aG docker "$(id -un)"

# Warm the Go module cache and build cache (matches the go.mod toolchain, Go 1.26).
go mod download
go build ./... >/dev/null
