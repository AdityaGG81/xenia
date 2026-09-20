#!/usr/bin/env bash
set -euo pipefail

NODE_NUM=${1:?Usage: ./start-node.sh <node-number: 1 or 2>}
PEER_NUM=$((3 - NODE_NUM))

echo "Starting IILCN Node $NODE_NUM..."
~/iilcn-node \
  --node-id "pi${NODE_NUM}" \
  --listen "0.0.0.0:8080" \
  --peer-url "http://10.20.0.${PEER_NUM}:8080" \
  --storage-dir "$HOME/iilcn-storage-n${NODE_NUM}"
