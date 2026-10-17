#!/usr/bin/env bash
set -u

echo '--- node service ---'
systemctl --no-pager --full status xenia-node.service 2>&1 | sed -n '1,16p' || true
echo '--- mesh interface and peers ---'
ip -brief address show 2>&1 | awk '$1 == "br0" || $1 == "bat0"' || true
batctl meshif bat0 interface 2>&1 || true
batctl meshif bat0 neighbors 2>&1 || true
batctl meshif bat0 originators 2>&1 || true
echo '--- phone AP and node health ---'
iw dev 2>&1 | sed -n '1,100p'
if command -v curl >/dev/null 2>&1; then
  NODE_IP=$(ip -4 -o address show dev br0 2>/dev/null | awk '{split($4, a, "/"); print a[1]; exit}')
  if [[ -n "$NODE_IP" && -f /etc/xenia-node/ca.crt ]]; then
    curl --fail --silent --show-error --max-time 3 --cacert /etc/xenia-node/ca.crt \
      "https://${NODE_IP}:8080/health" || true
  else
    curl --fail --silent --show-error --max-time 3 http://127.0.0.1:8080/health || true
  fi
  echo
fi
