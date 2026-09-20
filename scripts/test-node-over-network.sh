#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
node_binary="/tmp/iilcn-node"

(
  cd "$project_root/node"
  go build -buildvcs=false -o "$node_binary" ./cmd/iilcn-node
)

exec sudo python3 "$project_root/network/topology.py" diamond --fail-link n2-n3 --node-binary "$node_binary"
