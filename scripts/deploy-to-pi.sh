#!/usr/bin/env bash
# Deploy Xenia node binary to a Raspberry Pi via scp.
# Usage: ./scripts/deploy-to-pi.sh <user@host> [password]
set -euo pipefail

PI_SSH="${1:?Usage: deploy-to-pi.sh <user@host> [password]}"
PI_PASS="${2:-}"
project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

BINARY="$project_root/bin/pi/xenia-node"
if [ ! -f "$BINARY" ]; then
  echo "ERROR: Pi binary not found. Run: scripts/build-pi.sh first"
  exit 1
fi

SCP_CMD="scp -o StrictHostKeyChecking=no"
SSH_CMD="ssh -o StrictHostKeyChecking=no"
if [ -n "$PI_PASS" ]; then
  SCP_CMD="sshpass -p '$PI_PASS' $SCP_CMD"
  SSH_CMD="sshpass -p '$PI_PASS' $SSH_CMD"
fi

echo "==> Deploying xenia-node to $PI_SSH..."
eval $SCP_CMD "$BINARY" "$PI_SSH:/tmp/xenia-node"
eval $SSH_CMD "$PI_SSH" "chmod +x /tmp/xenia-node && echo 'Binary deployed to /tmp/xenia-node'"

echo "==> Installing batctl..."
eval $SSH_CMD "$PI_SSH" "sudo apt-get install -y batctl 2>/dev/null || echo 'batctl install failed — may need manual install'"

echo "==> Loading batman-adv kernel module..."
eval $SSH_CMD "$PI_SSH" "sudo modprobe batman-adv && echo 'batman-adv loaded OK'"

echo ""
echo "Deploy complete! Next: run setup-mesh-pi.sh on the Pi."
