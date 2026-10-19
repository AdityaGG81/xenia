#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-$ROOT_DIR/deployment/tls}"
if ! command -v openssl >/dev/null 2>&1; then
  echo "OpenSSL is required to create the local mesh CA and node certificates." >&2
  exit 1
fi
mkdir -p "$OUT_DIR"
if [[ ! -f "$OUT_DIR/ca.key" || ! -f "$OUT_DIR/ca.crt" ]]; then
  umask 077
  openssl req -x509 -newkey rsa:3072 -nodes -keyout "$OUT_DIR/ca.key" \
    -out "$OUT_DIR/ca.crt" -days 3650 -sha256 \
    -subj "/CN=Xenia Mesh Local CA" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -addext "subjectKeyIdentifier=hash"
fi

TARGET_NODES="${2:-}"
if [[ -n "$TARGET_NODES" ]]; then
  NODE_LIST=("$TARGET_NODES")
else
  NODE_LIST=(1 2 3 4 5 6 7 8)
fi

for node_id in "${NODE_LIST[@]}"; do
  node_ip="10.20.${node_id}.1"
  request="$OUT_DIR/node${node_id}.csr"
  extensions="$OUT_DIR/node${node_id}.ext"
  rm -f "$OUT_DIR/node${node_id}.key" "$OUT_DIR/node${node_id}.crt"
  openssl req -newkey rsa:2048 -nodes -keyout "$OUT_DIR/node${node_id}.key" \
    -out "$request" -subj "/CN=xenia.mesh"
  cat > "$extensions" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=IP:${node_ip},IP:10.20.0.${node_id},IP:10.20.0.1,IP:10.20.0.2,IP:10.20.0.3,IP:10.20.0.4,IP:10.20.1.1,IP:10.20.2.1,IP:10.20.3.1,DNS:xenia.mesh,DNS:*.xenia.mesh,DNS:xenia.local,DNS:*.xenia.local
EOF
  openssl x509 -req -in "$request" -CA "$OUT_DIR/ca.crt" \
    -CAkey "$OUT_DIR/ca.key" -CAcreateserial -out "$OUT_DIR/node${node_id}.crt" \
    -days 825 -sha256 -extfile "$extensions"
  rm -f "$request" "$extensions"
done

chmod 0600 "$OUT_DIR"/*.key
chmod 0644 "$OUT_DIR"/*.crt
echo "Created a private local CA and node certificates in $OUT_DIR for nodes: ${NODE_LIST[*]}."
echo "Keep ca.key private. Copy only ca.crt and each Pi's matching node certificate/key to that Pi."
