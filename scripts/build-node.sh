#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET="${1:---pi}"

case "$TARGET" in
  --pi)
    export GOOS=linux GOARCH=arm64 CGO_ENABLED=0
    OUTPUT="$ROOT_DIR/bin/pi/xenia-node"
    ;;
  --host)
    OUTPUT="$ROOT_DIR/bin/xenia-node"
    ;;
  *)
    echo "Usage: $0 [--pi|--host]" >&2
    exit 2
    ;;
esac

npm --prefix "$ROOT_DIR/client" run build
rm -rf "$ROOT_DIR/node/cmd/xenia-node/www"
mkdir -p "$ROOT_DIR/node/cmd/xenia-node/www" "$(dirname "$OUTPUT")"
cp -R "$ROOT_DIR/client/dist/." "$ROOT_DIR/node/cmd/xenia-node/www/"

if [[ "$TARGET" == "--pi" ]]; then
  (cd "$ROOT_DIR/node" && GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED="$CGO_ENABLED" go build -trimpath -buildvcs=false -ldflags='-s -w' -o "$OUTPUT" ./cmd/xenia-node)
else
  (cd "$ROOT_DIR/node" && go build -trimpath -buildvcs=false -ldflags='-s -w' -o "$OUTPUT" ./cmd/xenia-node)
fi

chmod 0755 "$OUTPUT"
echo "Built $OUTPUT"
