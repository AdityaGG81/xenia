#!/usr/bin/env bash
# Deploy Xenia to a single Raspberry Pi node (e.g. Node 3)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SECRETS_FILE="$ROOT_DIR/deployment/node-secrets.env"
TLS_DIR="$ROOT_DIR/deployment/tls"

if (( $# < 2 )); then
  echo "Usage: $0 <user@pi-ip> <node-id: 1–254>" >&2
  echo "Example: $0 pi@192.168.1.50 3" >&2
  exit 2
fi

TARGET="$1"
NODE_ID="$2"

if (( NODE_ID < 1 || NODE_ID > 254 )); then
  echo "Node ID must be between 1 and 254." >&2
  exit 2
fi

# Ensure secrets exist
if [[ ! -f "$SECRETS_FILE" ]]; then
  echo "Secrets file missing: $SECRETS_FILE. Run deploy-pi-pair.sh first to generate credentials." >&2
  exit 1
fi

# Ensure TLS certs exist for this node
if [[ ! -f "$TLS_DIR/ca.crt" || ! -f "$TLS_DIR/node${NODE_ID}.crt" || ! -f "$TLS_DIR/node${NODE_ID}.key" ]]; then
  echo "TLS certificates for Node ${NODE_ID} missing in $TLS_DIR. Generating..."
  "$ROOT_DIR/scripts/create-mesh-tls-certs.sh"
fi

# Build binary if not already built
if [[ ! -f "$ROOT_DIR/bin/pi/xenia-node" ]]; then
  echo "Building ARM64 binary..."
  "$ROOT_DIR/scripts/build-node.sh" --pi
fi

echo "Copying deployment files to $TARGET (Node $NODE_ID)..."
scp "$ROOT_DIR/bin/pi/xenia-node" "$TARGET:~/xenia-node.new"
scp "$ROOT_DIR/scripts/setup-mesh-pi.sh" \
    "$ROOT_DIR/scripts/mesh-status.sh" \
    "$ROOT_DIR/scripts/restore-network.sh" \
    "$ROOT_DIR/scripts/install-xenia-pi.sh" \
    "$SECRETS_FILE" \
    "$TLS_DIR/ca.crt" "$TLS_DIR/node${NODE_ID}.crt" "$TLS_DIR/node${NODE_ID}.key" \
    "$TARGET:~/"

ssh "$TARGET" 'mv -f ~/xenia-node.new ~/xenia-node && chmod 0755 ~/xenia-node ~/setup-mesh-pi.sh ~/mesh-status.sh ~/restore-network.sh ~/install-xenia-pi.sh && chmod 0600 ~/node*.key ~/node-secrets.env && (sudo systemctl restart xenia-node.service 2>/dev/null || true)'

echo ""
echo "Deployment files successfully copied to $TARGET!"
echo "If this is the FIRST TIME setting up Node $NODE_ID, run:"
echo "  ssh $TARGET \"sudo ~/install-xenia-pi.sh $NODE_ID\""
echo ""
echo "If Node $NODE_ID was already installed, xenia-node.service was restarted automatically."
