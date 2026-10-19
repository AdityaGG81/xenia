#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TLS_DIR="$ROOT_DIR/deployment/tls"
SECRETS_FILE="$ROOT_DIR/deployment/node-secrets.env"

if (( $# < 2 )); then
  echo "Usage: $0 <user@pi-address> <node-id: 1-254> [--no-run]" >&2
  echo "Example: $0 prachi46@192.168.1.25 3" >&2
  exit 2
fi

TARGET="$1"
NODE_ID="$2"
RUN_INSTALL=true
if [[ "${3:-}" == "--no-run" ]]; then
  RUN_INSTALL=false
fi

if [[ ! "$TARGET" =~ ^[A-Za-z0-9._-]+@[A-Za-z0-9._:-]+$ ]]; then
  echo "Invalid SSH target: $TARGET (expected user@host or user@IP)." >&2
  exit 2
fi

if [[ ! "$NODE_ID" =~ ^[0-9]{1,3}$ ]] || (( NODE_ID < 1 || NODE_ID > 254 )); then
  echo "Invalid Node ID: $NODE_ID (must be between 1 and 254)." >&2
  exit 2
fi

command -v ssh >/dev/null || { echo "ssh is required." >&2; exit 1; }
command -v scp >/dev/null || { echo "scp is required." >&2; exit 1; }
command -v openssl >/dev/null || { echo "openssl is required." >&2; exit 1; }

# 1. Ensure shared mesh secrets exist
if [[ ! -e "$SECRETS_FILE" ]]; then
  install -d -m 0700 "$(dirname "$SECRETS_FILE")"
  umask 077
  ap_password=$(openssl rand -hex 16)
  mesh_token=$(openssl rand -hex 32)
  {
    printf 'XENIA_AP_PASSWORD=%s\n' "$ap_password"
    printf 'XENIA_MESH_TOKEN=%s\n' "$mesh_token"
  } > "$SECRETS_FILE"
  chmod 0600 "$SECRETS_FILE"
fi

# 2. Ensure TLS certificates exist for this node
if [[ ! -f "$TLS_DIR/ca.crt" || ! -f "$TLS_DIR/node${NODE_ID}.crt" || ! -f "$TLS_DIR/node${NODE_ID}.key" ]]; then
  echo "Generating TLS credentials for node ${NODE_ID}..."
  "$ROOT_DIR/scripts/create-mesh-tls-certs.sh" "$TLS_DIR" "$NODE_ID"
fi

# 3. Ensure ARM/Pi binary is built
if [[ ! -f "$ROOT_DIR/bin/pi/xenia-node" ]]; then
  echo "Building Xenia node binary for Raspberry Pi (Linux ARM)..."
  "$ROOT_DIR/scripts/build-node.sh" --pi
fi

echo "Deploying files to Node ${NODE_ID} ($TARGET)..."
scp -q "$ROOT_DIR/bin/pi/xenia-node" "$TARGET:~/xenia-node.new"
scp -q "$ROOT_DIR/scripts/setup-mesh-pi.sh" \
  "$ROOT_DIR/scripts/mesh-status.sh" \
  "$ROOT_DIR/scripts/restore-network.sh" \
  "$ROOT_DIR/scripts/install-xenia-pi.sh" \
  "$SECRETS_FILE" \
  "$TLS_DIR/ca.crt" "$TLS_DIR/node${NODE_ID}.crt" "$TLS_DIR/node${NODE_ID}.key" \
  "$TARGET:~/"

ssh "$TARGET" "mv -f ~/xenia-node.new ~/xenia-node && chmod 0755 ~/xenia-node ~/setup-mesh-pi.sh ~/mesh-status.sh ~/restore-network.sh ~/install-xenia-pi.sh && chmod 0600 ~/node*.key ~/node-secrets.env"

echo "Files copied successfully."

if [[ "$RUN_INSTALL" == true ]]; then
  echo "Configuring Node ${NODE_ID} via install-xenia-pi.sh..."
  ssh -t "$TARGET" "sudo ~/install-xenia-pi.sh $NODE_ID"
  echo ""
  echo "=================================================="
  echo "Node ${NODE_ID} is fully configured and active!"
  echo "=================================================="
fi
