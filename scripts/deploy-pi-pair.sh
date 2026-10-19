#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TLS_DIR="$ROOT_DIR/deployment/tls"
SECRETS_FILE="$ROOT_DIR/deployment/node-secrets.env"
if (( $# != 2 )); then
  echo "Usage: $0 <user@pi-1-address> <user@pi-2-address>" >&2
  exit 2
fi
PI1="$1"
PI2="$2"
for target in "$PI1" "$PI2"; do
  if [[ ! "$target" =~ ^[A-Za-z0-9._-]+@[A-Za-z0-9._:-]+$ ]]; then
    echo "Invalid SSH target: $target (expected user@host or user@IPv4-address)." >&2
    exit 2
  fi
done
command -v ssh >/dev/null || { echo "ssh is required." >&2; exit 1; }
command -v scp >/dev/null || { echo "scp is required." >&2; exit 1; }
command -v openssl >/dev/null || { echo "openssl is required to generate node credentials." >&2; exit 1; }

if [[ ! -e "$SECRETS_FILE" ]]; then
  install -d -m 0700 "$(dirname "$SECRETS_FILE")"
  umask 077
  ap_password=$(openssl rand -hex 16)
  mesh_token=$(openssl rand -hex 32)
  {
    printf 'XENIA_AP_PASSWORD=%s\n' "$ap_password"
    printf 'XENIA_MESH_TOKEN=%s\n' "$mesh_token"
  } > "$SECRETS_FILE"
elif [[ $(grep -c '^XENIA_AP_PASSWORD=' "$SECRETS_FILE") != 1 || \
       $(grep -c '^XENIA_MESH_TOKEN=' "$SECRETS_FILE") != 1 ]] || \
  ! grep -Eq '^XENIA_AP_PASSWORD=[A-Fa-f0-9]{16,63}$' "$SECRETS_FILE" || \
  ! grep -Eq '^XENIA_MESH_TOKEN=[A-Fa-f0-9]{32,128}$' "$SECRETS_FILE"; then
  echo "Credentials file is malformed: $SECRETS_FILE. Move it aside to generate new credentials." >&2
  exit 1
fi
chmod 0600 "$SECRETS_FILE"
ap_password=$(sed -n 's/^XENIA_AP_PASSWORD=//p' "$SECRETS_FILE")

tls_files=(ca.crt node1.key node1.crt node2.key node2.crt)
found_tls=0
for file in "${tls_files[@]}"; do
  [[ ! -e "$TLS_DIR/$file" ]] || found_tls=$((found_tls + 1))
done
if (( found_tls == 0 )); then
  "$ROOT_DIR/scripts/create-mesh-tls-certs.sh"
elif (( found_tls != ${#tls_files[@]} )); then
  echo "TLS directory is incomplete: $TLS_DIR. Complete or move it before deploying." >&2
  exit 1
fi
chmod 0600 "$TLS_DIR/node1.key" "$TLS_DIR/node2.key"
if [[ -f "$TLS_DIR/ca.key" ]]; then chmod 0600 "$TLS_DIR/ca.key"; fi

"$ROOT_DIR/scripts/build-node.sh" --pi

copy_to_pi() {
  local target="$1" node_id="$2"
  scp "$ROOT_DIR/bin/pi/xenia-node" "$target:~/xenia-node.new"
  scp "$ROOT_DIR/scripts/setup-mesh-pi.sh" \
    "$ROOT_DIR/scripts/mesh-status.sh" \
    "$ROOT_DIR/scripts/restore-network.sh" \
    "$ROOT_DIR/scripts/install-xenia-pi.sh" \
    "$SECRETS_FILE" \
    "$TLS_DIR/ca.crt" "$TLS_DIR/node${node_id}.crt" "$TLS_DIR/node${node_id}.key" \
    "$target:~/"
  ssh "$target" 'mv -f ~/xenia-node.new ~/xenia-node && chmod 0755 ~/xenia-node ~/setup-mesh-pi.sh ~/mesh-status.sh ~/restore-network.sh ~/install-xenia-pi.sh && chmod 0600 ~/node*.key ~/node-secrets.env && (sudo systemctl restart xenia-node.service 2>/dev/null || true)'
}

echo "Copying node 1 deployment to $PI1..."
copy_to_pi "$PI1" 1
echo "Copying node 2 deployment to $PI2..."
copy_to_pi "$PI2" 2

cat <<EOF

Files are deployed. Run setup from a local console or wired SSH session on each Pi;
setup changes Wi-Fi and disables NetworkManager, so a Wi-Fi SSH session will drop.

Pi 1: sudo ~/install-xenia-pi.sh 1
Pi 2: sudo ~/install-xenia-pi.sh 2

No matching passwords or tokens need to be entered on the Pis.
EOF
cat <<EOF
The mesh link itself is open to avoid adapter compatibility problems. The phone APs
remain WPA2-protected. The generated AP password (same on both Pis) is:

$ap_password

Keep $TLS_DIR/ca.key private. Import $TLS_DIR/ca.crt on both phones to enable microphone access.
EOF
