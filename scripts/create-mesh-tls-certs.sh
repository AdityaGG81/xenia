#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-$ROOT_DIR/deployment/tls}"
if ! command -v openssl >/dev/null 2>&1; then
  echo "OpenSSL is required to create the local mesh CA and node certificates." >&2
  exit 1
fi
mkdir -p "$OUT_DIR"
for file in ca.key ca.crt node1.key node1.crt node2.key node2.crt; do
  if [[ -e "$OUT_DIR/$file" ]]; then
    echo "Refusing to overwrite $OUT_DIR/$file" >&2
    exit 1
  fi
done

umask 077
openssl req -x509 -newkey rsa:3072 -nodes -keyout "$OUT_DIR/ca.key" \
  -out "$OUT_DIR/ca.crt" -days 3650 -sha256 \
  -subj "/CN=Xenia Mesh Local CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -addext "subjectKeyIdentifier=hash"

for node_id in 1 2; do
  node_ip="10.20.${node_id}.1"
  request="$OUT_DIR/node${node_id}.csr"
  extensions="$OUT_DIR/node${node_id}.ext"
  openssl req -newkey rsa:2048 -nodes -keyout "$OUT_DIR/node${node_id}.key" \
    -out "$request" -subj "/CN=${node_ip}"
  cat > "$extensions" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=IP:${node_ip}
EOF
  openssl x509 -req -in "$request" -CA "$OUT_DIR/ca.crt" \
    -CAkey "$OUT_DIR/ca.key" -CAcreateserial -out "$OUT_DIR/node${node_id}.crt" \
    -days 825 -sha256 -extfile "$extensions"
  rm -f "$request" "$extensions"
done

chmod 0600 "$OUT_DIR/ca.key" "$OUT_DIR/node1.key" "$OUT_DIR/node2.key"
chmod 0644 "$OUT_DIR/ca.crt" "$OUT_DIR/node1.crt" "$OUT_DIR/node2.crt"
echo "Created a private local CA and node certificates in $OUT_DIR."
echo "Keep ca.key private. Copy only ca.crt and each Pi's matching node certificate/key to that Pi."
