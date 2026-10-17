#!/usr/bin/env bash
# Compatibility entry point; Xenia nodes now use a BATMAN-adv mesh, not a tree.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$SCRIPT_DIR/setup-mesh-pi.sh" "$@"
