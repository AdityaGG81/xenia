#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
node_binary="/tmp/xenia-node"
storage_dir=$(mktemp -d)

(
  cd "$project_root/node"
  go build -buildvcs=false -o "$node_binary" ./cmd/xenia-node
)

echo "== Starting node with storage dir: $storage_dir =="
"$node_binary" --node-id n1 --listen 127.0.0.1:8080 --storage-dir "$storage_dir" > /tmp/xenia-offline.log 2>&1 &
NODE_PID=$!

# Wait for healthy
for i in {1..10}; do
  if curl --silent --fail http://127.0.0.1:8080/health | grep -q '"status":"ok"'; then
    break
  fi
  sleep 0.2
done

echo "== Sending message to node =="
curl --silent --fail -X POST http://127.0.0.1:8080/message \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","to":"bob","body":"offline test"}' > /dev/null

echo "== Verifying message is in memory =="
inbox=$(curl --silent --fail http://127.0.0.1:8080/messages/bob)
if ! echo "$inbox" | grep -q '"body":"offline test"'; then
  echo "FAIL: Message not found in inbox"
  kill $NODE_PID
  exit 1
fi

echo "== Terminating node =="
kill $NODE_PID
wait $NODE_PID || true

echo "== Restarting node with same storage dir =="
"$node_binary" --node-id n1 --listen 127.0.0.1:8080 --storage-dir "$storage_dir" > /tmp/xenia-offline.log 2>&1 &
NODE_PID=$!

for i in {1..10}; do
  if curl --silent --fail http://127.0.0.1:8080/health | grep -q '"status":"ok"'; then
    break
  fi
  sleep 0.2
done

echo "== Verifying message survived restart =="
inbox=$(curl --silent --fail http://127.0.0.1:8080/messages/bob)
if ! echo "$inbox" | grep -q '"body":"offline test"'; then
  echo "FAIL: Message did not survive restart. Inbox: $inbox"
  kill $NODE_PID
  exit 1
fi
echo "PASS: Message successfully recovered from disk!"

echo "== Cleaning up =="
kill $NODE_PID
wait $NODE_PID || true
rm -rf "$storage_dir"
