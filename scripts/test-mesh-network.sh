#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "== Testing BATMAN-adv Mesh Routing =="
exec sudo python3 "$project_root/network/topology.py" mesh --fail-link n2-n3
