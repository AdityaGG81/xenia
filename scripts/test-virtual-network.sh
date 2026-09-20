#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ $# -eq 0 ]]; then
  echo "Usage: $0 {linear|diamond} [--fail-link n2-n3]" >&2
  exit 2
fi

exec sudo python3 "$project_root/network/topology.py" "$@"
