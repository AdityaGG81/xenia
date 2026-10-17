f#!/usr/bin/env bash
# Sets up a Xenia offline node in the Chained Wi-Fi Tree Architecture.

set -euo pipefail
export PATH="/usr/sbin:/usr/local/sbin:$PATH"

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root (sudo)"
  exit 1
fi

if [ "$#" -ne 2 ]; then
  echo "Usage: sudo $0 <MyNodeID> <UplinkNodeID>"
  echo "Example Root Node: sudo $0 1 0"
  echo "Example Child Node: sudo $0 2 1"
  exit 1
fi

MY_ID=$1
UPLINK_ID=$2

AP_IFACE="wlan0"
CLIENT_IFACE="wlan1"
MY_SUBNET="192.168.4${MY_ID}"
MY_AP_IP="${MY_SUBNET}.1"
MY_SSID="Xenia-Node-${MY_ID}"

echo "=== Xenia Node Setup: Node ${MY_ID} ==="
echo "Access Point: ${MY_SSID} (${MY_AP_IP})"
if [ "$UPLINK_ID" -eq 0 ]; then
  echo "Role: ROOT Node (No uplink)"
else
  echo "Role: CHILD Node (Connecting to Xenia-Node-${UPLINK_ID})"
fi

echo "[1/6] Stopping network managers to take control..."
systemctl stop NetworkManager 2>/dev/null || true
systemctl disable NetworkManager 2>/dev/null || true
rfkill unblock wlan

echo "[2/6] Configuring DHCP client ignores..."
# Prevent dhcpcd from assigning IPs to the Access Point interface
grep -q "denyinterfaces ${AP_IFACE}" /etc/dhcpcd.conf || echo "denyinterfaces ${AP_IFACE}" >> /etc/dhcpcd.conf
systemctl restart dhcpcd || true

echo "[3/6] Configuring Access Point (${AP_IFACE})..."
ip link set dev $AP_IFACE up
ip addr flush dev $AP_IFACE
ip addr add ${MY_AP_IP}/24 dev $AP_IFACE

# DHCP Server (dnsmasq) for the Access Point
mv /etc/dnsmasq.conf /etc/dnsmasq.conf.orig 2>/dev/null || true
cat > /etc/dnsmasq.conf <<EOF
interface=${AP_IFACE}
dhcp-range=${MY_SUBNET}.10,${MY_SUBNET}.50,255.255.255.0,24h
EOF

if [ "$MY_ID" -eq 1 ]; then
  AP_CHANNEL=1
elif [ "$MY_ID" -eq 2 ]; then
  AP_CHANNEL=11
else
  AP_CHANNEL=6
fi

# Wi-Fi AP (hostapd)
cat > /etc/hostapd/hostapd.conf <<EOF
interface=${AP_IFACE}
driver=nl80211
ssid=${MY_SSID}
hw_mode=g
channel=${AP_CHANNEL}
macaddr_acl=0
auth_algs=1
ignore_broadcast_ssid=0
wpa=2
wpa_passphrase=xeniamesh
wpa_key_mgmt=WPA-PSK
wpa_pairwise=TKIP CCMP
rsn_pairwise=CCMP
country_code=IN
ieee80211d=1
EOF
sed -i 's|#DAEMON_CONF=""|DAEMON_CONF="/etc/hostapd/hostapd.conf"|g' /etc/default/hostapd 2>/dev/null || true

# Force systemd to use our config file (fixes silent failures on fresh installs)
mkdir -p /etc/systemd/system/hostapd.service.d
cat > /etc/systemd/system/hostapd.service.d/override.conf <<EOF
[Service]
ExecStart=
ExecStart=/usr/sbin/hostapd -B -P /run/hostapd.pid /etc/hostapd/hostapd.conf
EOF
systemctl daemon-reload

if [ "$UPLINK_ID" -ne 0 ]; then
  echo "[4/6] Configuring Client connection to Uplink Node ${UPLINK_ID} (${CLIENT_IFACE})..."
  UPLINK_SSID="Xenia-Node-${UPLINK_ID}"
  
  # Create wpa_supplicant config
  cat > /etc/wpa_supplicant/wpa_supplicant-${CLIENT_IFACE}.conf <<EOF
ctrl_interface=DIR=/var/run/wpa_supplicant GROUP=netdev
update_config=1
country=IN

network={
    ssid="${UPLINK_SSID}"
    psk="xeniamesh"
    key_mgmt=WPA-PSK
}
EOF
  # Enable and start wpa_supplicant specifically for wlan1
  systemctl unmask wpa_supplicant@${CLIENT_IFACE} 2>/dev/null || true
  systemctl enable wpa_supplicant@${CLIENT_IFACE} 2>/dev/null || true
  systemctl restart wpa_supplicant@${CLIENT_IFACE} 2>/dev/null || true
  
  echo "Assigning static IP to ${CLIENT_IFACE} so it can route..."
  ip link set dev $CLIENT_IFACE up
  ip addr flush dev $CLIENT_IFACE
  ip addr add 192.168.4${UPLINK_ID}.${MY_ID}/24 dev $CLIENT_IFACE
else
  echo "[4/6] Skipping Uplink Client configuration (Root Node)..."
fi

echo "[5/6] Starting Access Point services..."
systemctl unmask hostapd
systemctl enable hostapd
systemctl enable dnsmasq

systemctl restart dnsmasq
systemctl restart hostapd

echo "[6/6] Node setup complete!"
echo "If this script was run via SSH, you will now lose connection to the home network."
echo "Connect to ${MY_SSID} (Password: xeniamesh) and SSH to ${MY_AP_IP} to access this node!"

echo "[7/7] Starting Xenia Node Server..."
pkill xenia-node || true

# Add a default route for UDP broadcasts so they don't fail with 'network unreachable'
if [ "$MY_ID" -eq 1 ]; then
    ip route add 255.255.255.255 dev $AP_IFACE 2>/dev/null || true
else
    ip route add 255.255.255.255 dev $CLIENT_IFACE 2>/dev/null || true
fi

USER_HOME=$(eval echo ~$SUDO_USER)
nohup ${USER_HOME}/xenia-node -listen 0.0.0.0:8080 -node-id node${MY_ID} > /tmp/xenia-node.log 2>&1 &
echo "Mesh is online! UDP Auto-Discovery is running!"
