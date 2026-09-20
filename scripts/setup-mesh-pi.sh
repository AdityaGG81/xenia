#!/usr/bin/env bash
# Configure BATMAN-adv mesh over Wi-Fi on a Raspberry Pi.
# Usage: sudo ./setup-mesh-pi.sh <node-number: 1 or 2>
#
# This script:
#   1. Disconnects wlan0 from any managed network
#   2. Sets wlan0 to IBSS (ad-hoc) mode
#   3. Joins the "iilcn-mesh" ad-hoc cell
#   4. Layers BATMAN-adv on top via bat0
#   5. Assigns a mesh IP (10.20.0.<node-number>)
set -euo pipefail
export PATH="/usr/sbin:/usr/local/sbin:$PATH"

NODE_NUM=${1:?Usage: sudo ./setup-mesh-pi.sh <node-number: 1 or 2>}

if [ "$EUID" -ne 0 ]; then
  echo "ERROR: This script must be run as root (use sudo)."
  exit 1
fi

MESH_IP="10.20.0.${NODE_NUM}/24"
MESH_SSID="iilcn-mesh"
MESH_FREQ=2412  # Channel 1 (2.4 GHz)
MESH_BSSID="02:12:34:56:78:9A"  # Fixed BSSID so both Pis join the same cell

echo "=== IILCN Mesh Setup: Node $NODE_NUM ==="
echo "    Mesh IP:   $MESH_IP"
echo "    SSID:      $MESH_SSID"
echo "    Frequency: $MESH_FREQ MHz (Channel 1)"
echo ""

# Step 1: Stop services that manage wlan0
echo "[1/6] Stopping network managers..."
systemctl stop NetworkManager 2>/dev/null || true
systemctl stop wpa_supplicant 2>/dev/null || true
killall wpa_supplicant 2>/dev/null || true
sleep 1

# Step 2: Bring wlan0 down and set to IBSS mode
echo "[2/6] Configuring wlan0 for ad-hoc mode..."
ip link set wlan0 down
iw wlan0 set type ibss
ip link set wlan0 up
sleep 1

# Step 3: Join ad-hoc cell
echo "[3/6] Joining ad-hoc cell '$MESH_SSID'..."
iw wlan0 ibss join "$MESH_SSID" "$MESH_FREQ"
sleep 2

# Step 4: Load batman-adv
echo "[4/6] Loading batman-adv..."
modprobe batman-adv
batctl if add wlan0

# Step 5: Bring up bat0 and assign mesh IP
echo "[5/6] Configuring bat0 with $MESH_IP..."
ip link set bat0 up
ip addr flush dev bat0
ip addr add "$MESH_IP" dev bat0

# Step 6: Disable features that cause issues on simple setups
echo "[6/6] Tuning BATMAN-adv parameters..."
batctl dat off 2>/dev/null || true
batctl mm off 2>/dev/null || true
batctl bl off 2>/dev/null || true

echo ""
echo "============================================================"
echo "  Mesh Node $NODE_NUM is READY!"
echo "============================================================"
echo "  bat0 IP:    $MESH_IP"
echo "  Verify:     batctl n       (should show the other Pi)"
echo "  Ping peer:  ping 10.20.0.$((3 - NODE_NUM))"
echo ""
echo "  To start the IILCN node service:"
echo "    /tmp/iilcn-node --node-id pi${NODE_NUM} --listen 0.0.0.0:8080 \\"
echo "      --peer-url http://10.20.0.$((3 - NODE_NUM)):8080 \\"
echo "      --storage-dir /tmp/iilcn-storage"
echo "============================================================"
