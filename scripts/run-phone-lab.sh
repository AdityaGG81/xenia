#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
node_binary="/tmp/iilcn-node"
proxy_binary="/tmp/iilcn-access-proxy"

(
  cd "$project_root/node"
  go build -buildvcs=false -o "$node_binary" ./cmd/iilcn-node
  go build -buildvcs=false -o "$proxy_binary" ./cmd/iilcn-access-proxy
)

exec sudo python3 "$project_root/network/phone_lab.py" --node-binary "$node_binary" --proxy-binary "$proxy_binary"
