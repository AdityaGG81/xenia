#!/usr/bin/env bash
set -euo pipefail

if (( EUID != 0 )); then
  echo "Run with sudo: sudo $0 <node-id>" >&2
  exit 1
fi
if (( $# != 1 )) || [[ ! "$1" =~ ^[0-9]{1,3}$ ]]; then
  echo "Usage: sudo $0 <node-id: 1–254>" >&2
  exit 2
fi
NODE_ID=$((10#$1))
if (( NODE_ID < 1 || NODE_ID > 254 )); then
  echo "Node ID must be 1–254." >&2
  exit 2
fi
if [[ -z "${SUDO_USER:-}" ]] || ! getent passwd "$SUDO_USER" >/dev/null; then
  echo "Run with sudo from the account that received the deployment files." >&2
  exit 2
fi

NODE_HOME=$(getent passwd "$SUDO_USER" | cut -d: -f6)
NODE_GROUP=$(id -gn "$SUDO_USER")
for file in xenia-node setup-mesh-pi.sh mesh-status.sh restore-network.sh \
  ca.crt node-secrets.env "node${NODE_ID}.crt" "node${NODE_ID}.key"; do
  if [[ ! -f "$NODE_HOME/$file" ]]; then
    echo "Missing $NODE_HOME/$file. Run deploy-pi-pair.sh from the development computer first." >&2
    exit 1
  fi
done

if [[ ! -r "$NODE_HOME/node-secrets.env" ]]; then
  echo "Cannot read provisioned credentials: $NODE_HOME/node-secrets.env" >&2
  exit 1
fi
if [[ $(grep -c '^XENIA_AP_PASSWORD=' "$NODE_HOME/node-secrets.env") != 1 || \
      $(grep -c '^XENIA_MESH_TOKEN=' "$NODE_HOME/node-secrets.env") != 1 ]]; then
  echo "Provisioned credentials file is malformed." >&2
  exit 1
fi
XENIA_AP_PASSWORD=$(sed -n 's/^XENIA_AP_PASSWORD=//p' "$NODE_HOME/node-secrets.env")
XENIA_MESH_TOKEN=$(sed -n 's/^XENIA_MESH_TOKEN=//p' "$NODE_HOME/node-secrets.env")
if [[ ! "$XENIA_AP_PASSWORD" =~ ^[A-Fa-f0-9]{16,63}$ || \
      ! "$XENIA_MESH_TOKEN" =~ ^[A-Fa-f0-9]{32,128}$ ]]; then
  echo "Provisioned credentials are incomplete." >&2
  exit 1
fi
export XENIA_AP_PASSWORD XENIA_MESH_TOKEN

install -d -m 0710 -o root -g "$NODE_GROUP" /etc/xenia-node
install -o root -g root -m 0644 "$NODE_HOME/ca.crt" /etc/xenia-node/ca.crt
install -o "$SUDO_USER" -g "$NODE_GROUP" -m 0644 \
  "$NODE_HOME/node${NODE_ID}.crt" /etc/xenia-node/tls.crt
install -o "$SUDO_USER" -g "$NODE_GROUP" -m 0600 \
  "$NODE_HOME/node${NODE_ID}.key" /etc/xenia-node/tls.key

exec "$NODE_HOME/setup-mesh-pi.sh" "$NODE_ID"
