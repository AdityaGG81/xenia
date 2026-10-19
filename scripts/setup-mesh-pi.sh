#!/usr/bin/env bash
# Configure Xenia mesh backhaul (BATMAN-adv IBSS or Managed Wi-Fi Uplink) and phone AP.
set -euo pipefail
export PATH="/usr/sbin:/usr/local/sbin:$PATH"

if (( EUID != 0 )); then echo "Run with sudo: $0 <node-id>" >&2; exit 1; fi
if (( $# != 1 )) || [[ ! "$1" =~ ^[0-9]{1,3}$ ]]; then
  echo "Usage: sudo $0 <node-id: 1–254>" >&2; exit 2
fi
NODE_ID=$((10#$1))
if (( NODE_ID < 1 || NODE_ID > 254 )); then echo "Node ID must be 1–254." >&2; exit 2; fi
if [[ -z "${SUDO_USER:-}" ]] || ! getent passwd "$SUDO_USER" >/dev/null; then
  echo "Run with sudo from the account that owns ~/xenia-node." >&2; exit 2
fi
NODE_HOME=$(getent passwd "$SUDO_USER" | cut -d: -f6)
NODE_BIN="$NODE_HOME/xenia-node"
NODE_GROUP=$(id -gn "$SUDO_USER")
MESH_IFACE=${XENIA_MESH_IFACE:-wlan1}
AP_IFACE=${XENIA_AP_IFACE:-wlan0}
MESH_ID=${XENIA_MESH_ID:-XeniaMesh}
MESH_CHANNEL=${XENIA_MESH_CHANNEL:-1}

for iface in "$MESH_IFACE" "$AP_IFACE"; do
  if [[ ! "$iface" =~ ^[A-Za-z0-9_.-]{1,15}$ ]]; then
    echo "Wireless interface names must be 1–15 letters, digits, dots, underscores, or dashes." >&2
    exit 2
  fi
done
if [[ ! "$MESH_ID" =~ ^[A-Za-z0-9_-]{1,32}$ ]]; then
  echo "XENIA_MESH_ID must be 1–32 letters, digits, underscores, or dashes." >&2
  exit 2
fi
if [[ ! "$MESH_CHANNEL" =~ ^[0-9]{1,2}$ ]] || (( MESH_CHANNEL < 1 || MESH_CHANNEL > 11 )); then
  echo "XENIA_MESH_CHANNEL must be 1–11." >&2; exit 2
fi
MESH_FREQ=$((2407 + MESH_CHANNEL * 5))
AP_CHANNEL=6
if (( MESH_CHANNEL == 6 )); then AP_CHANNEL=11; fi
if (( NODE_ID == 1 )); then AP_CHANNEL=1; fi
NODE_IP="10.20.${NODE_ID}.1"

if [[ ! -x "$NODE_BIN" ]]; then echo "Expected executable Pi binary: $NODE_BIN" >&2; exit 1; fi
for command in iw ip hostapd dnsmasq wpa_supplicant wpa_cli systemctl sysctl; do
  command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 1; }
done
for iface in "$MESH_IFACE" "$AP_IFACE"; do
  [[ -e "/sys/class/net/$iface/phy80211" ]] || { echo "Wireless interface missing: $iface" >&2; exit 1; }
done
MESH_PHY=$(basename "$(readlink -f "/sys/class/net/$MESH_IFACE/phy80211")")
AP_PHY=$(basename "$(readlink -f "/sys/class/net/$AP_IFACE/phy80211")")
[[ "$MESH_PHY" != "$AP_PHY" ]] || { echo "Use separate mesh and AP radios." >&2; exit 1; }

# Check backhaul capability: IBSS (BATMAN-adv) vs Managed Client Uplink
BACKHAUL_MODE="ibss"
if ! iw phy "$MESH_PHY" info | grep -Eq '^\s+\* IBSS$'; then
  BACKHAUL_MODE="client"
  echo "Notice: $MESH_IFACE ($MESH_PHY) does not support IBSS mode."
  echo "Configuring Managed Wi-Fi Uplink mode for node ${NODE_ID}."
fi

iw phy "$AP_PHY" info | grep -Eq '^\s+\* AP$' || { echo "$AP_IFACE lacks AP mode." >&2; exit 1; }

if [[ "$BACKHAUL_MODE" == "ibss" ]]; then
  has_channel() {
    iw phy "$1" info | awk -v freq="$2" -v channel="[$3]" \
      '$1 == "*" && $2 == freq && $4 == channel && $0 !~ /disabled/ { found=1 } END { exit !found }'
  }
  has_channel "$MESH_PHY" "$MESH_FREQ" "$MESH_CHANNEL" || { echo "Mesh radio cannot use channel $MESH_CHANNEL." >&2; exit 1; }
  modprobe -n batman_adv >/dev/null 2>&1 || { echo "Kernel module batman_adv is unavailable." >&2; exit 1; }
fi

if [[ -z "${XENIA_AP_PASSWORD:-}" ]] || (( ${#XENIA_AP_PASSWORD} < 8 || ${#XENIA_AP_PASSWORD} > 63 )) || [[ ! "$XENIA_AP_PASSWORD" =~ ^[A-Za-z0-9_-]+$ ]]; then
  echo "Missing valid XENIA_AP_PASSWORD. Run install-xenia-pi.sh to provision generated credentials." >&2; exit 2
fi
if [[ -z "${XENIA_MESH_TOKEN:-}" ]] || (( ${#XENIA_MESH_TOKEN} < 32 || ${#XENIA_MESH_TOKEN} > 128 )) || [[ ! "$XENIA_MESH_TOKEN" =~ ^[A-Za-z0-9_-]+$ ]]; then
  echo "Missing valid XENIA_MESH_TOKEN. Run install-xenia-pi.sh to provision generated credentials." >&2; exit 2
fi
if [[ -e /etc/xenia-node/tls.crt || -e /etc/xenia-node/tls.key ]]; then
  [[ -f /etc/xenia-node/tls.crt && -f /etc/xenia-node/tls.key ]] || { echo "Provide both tls.crt and tls.key." >&2; exit 2; }
  [[ -f /etc/xenia-node/ca.crt ]] || { echo "Provide the signing CA as /etc/xenia-node/ca.crt for HTTPS peer signaling." >&2; exit 2; }
fi

backup_once() {
  local path="$1"
  [[ ! -e "$path" || -e "${path}.xenia-original" ]] || cp -a "$path" "${path}.xenia-original"
}
backup_once /etc/dhcpcd.conf
backup_once /etc/dnsmasq.conf
backup_once /etc/hostapd/hostapd.conf
backup_once "/etc/wpa_supplicant/wpa_supplicant-${MESH_IFACE}.conf"

install -d -m 0710 -o root -g "$NODE_GROUP" /etc/xenia-node
NODE_ENV_TMP=$(mktemp /etc/xenia-node/node.env.XXXXXX)
if [[ -f /etc/xenia-node/node.env ]]; then awk '!/^XENIA_MESH_TOKEN=/' /etc/xenia-node/node.env > "$NODE_ENV_TMP"; fi
printf 'XENIA_MESH_TOKEN=%s\n' "$XENIA_MESH_TOKEN" >> "$NODE_ENV_TMP"
chmod 0600 "$NODE_ENV_TMP"
mv "$NODE_ENV_TMP" /etc/xenia-node/node.env

# ──────────────────────────────────────────────
#  Hostapd & Dnsmasq Configuration
# ──────────────────────────────────────────────
AP_BIND_IFACE="${AP_IFACE}"
BRIDGE_LINE=""
if [[ "$BACKHAUL_MODE" == "ibss" ]]; then
  AP_BIND_IFACE="br0"
  BRIDGE_LINE="bridge=br0"
fi

cat > /etc/hostapd/hostapd.conf <<EOF
interface=${AP_IFACE}
${BRIDGE_LINE}
driver=nl80211
ssid=Xenia-${NODE_ID}
hw_mode=g
channel=${AP_CHANNEL}
macaddr_acl=0
auth_algs=1
ignore_broadcast_ssid=0
wpa=2
wpa_passphrase=${XENIA_AP_PASSWORD}
wpa_key_mgmt=WPA-PSK
wpa_pairwise=TKIP CCMP
rsn_pairwise=CCMP
country_code=IN
ieee80211d=1
EOF
chmod 0600 /etc/hostapd/hostapd.conf

sed -i 's|#DAEMON_CONF=""|DAEMON_CONF="/etc/hostapd/hostapd.conf"|g' /etc/default/hostapd 2>/dev/null || true
sed -i 's|DAEMON_CONF=.*|DAEMON_CONF="/etc/hostapd/hostapd.conf"|g' /etc/default/hostapd 2>/dev/null || true
rm -f /etc/systemd/system/hostapd.service.d/override.conf 2>/dev/null || true
rfkill unblock wlan 2>/dev/null || true

cat > /etc/dnsmasq.conf <<EOF
interface=${AP_BIND_IFACE}
bind-interfaces
dhcp-range=10.20.${NODE_ID}.100,10.20.${NODE_ID}.250,255.255.0.0,12h
dhcp-option=option:router,${NODE_IP}
dhcp-leasefile=/var/lib/dnsmasq/xenia-${NODE_ID}.leases
EOF
install -d -m 0755 /var/lib/dnsmasq

# ──────────────────────────────────────────────
#  Wpa_supplicant for Backhaul
# ──────────────────────────────────────────────
if [[ "$BACKHAUL_MODE" == "ibss" ]]; then
  cat > "/etc/wpa_supplicant/wpa_supplicant-${MESH_IFACE}.conf" <<EOF
ctrl_interface=DIR=/run/wpa_supplicant GROUP=netdev
country=IN
network={
    ssid="${MESH_ID}"
    mode=1
    frequency=${MESH_FREQ}
    key_mgmt=NONE
}
EOF
else
  # In Managed mode, child nodes connect to Node 1's AP
  if (( NODE_ID > 1 )); then
    cat > "/etc/wpa_supplicant/wpa_supplicant-${MESH_IFACE}.conf" <<EOF
ctrl_interface=DIR=/run/wpa_supplicant GROUP=netdev
country=IN
network={
    ssid="Xenia-1"
    psk="${XENIA_AP_PASSWORD}"
    key_mgmt=WPA-PSK
}
EOF
  fi
fi
if [[ -f "/etc/wpa_supplicant/wpa_supplicant-${MESH_IFACE}.conf" ]]; then
  chmod 0600 "/etc/wpa_supplicant/wpa_supplicant-${MESH_IFACE}.conf"
fi

# ──────────────────────────────────────────────
#  Mesh Network Startup Script
# ──────────────────────────────────────────────
if [[ "$BACKHAUL_MODE" == "ibss" ]]; then
  cat > /usr/local/sbin/xenia-mesh-net <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
NODE_IP="@NODE_IP@"
MESH_IFACE="@MESH_IFACE@"
case "${1:-start}" in
  start)
    mesh_ready=false
    for _ in $(seq 1 30); do
      if wpa_cli -i "$MESH_IFACE" status 2>/dev/null | grep -q '^wpa_state=COMPLETED$'; then
        mesh_ready=true
        break
      fi
      sleep 1
    done
    if [[ "$mesh_ready" != true ]]; then
      echo "IBSS link is not ready on $MESH_IFACE." >&2
      exit 1
    fi
    modprobe batman_adv
    ip link set "$MESH_IFACE" up
    ip link show bat0 >/dev/null 2>&1 || ip link add name bat0 type batadv
    batctl meshif bat0 interface -M add "$MESH_IFACE"
    ip link show br0 >/dev/null 2>&1 || ip link add name br0 type bridge
    ip link set bat0 master br0
    ip addr flush dev br0
    ip addr add "${NODE_IP}/16" dev br0
    ip link set bat0 up
    ip link set br0 up
    ;;
  stop)
    ip link set br0 down 2>/dev/null || true
    ip link set bat0 down 2>/dev/null || true
    ip link del br0 type bridge 2>/dev/null || true
    ip link del bat0 type batadv 2>/dev/null || true
    ;;
  *) echo "Usage: $0 [start|stop]" >&2; exit 2 ;;
esac
EOF
else
  # Client backhaul mode with static IP routing
  cat > /usr/local/sbin/xenia-mesh-net <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
NODE_ID="@NODE_ID@"
NODE_IP="@NODE_IP@"
MESH_IFACE="@MESH_IFACE@"
AP_IFACE="@AP_IFACE@"
case "${1:-start}" in
  start)
    sysctl -w net.ipv4.ip_forward=1 >/dev/null
    iptables -P FORWARD ACCEPT 2>/dev/null || true
    ip link set "$AP_IFACE" up
    ip addr flush dev "$AP_IFACE"
    ip addr add "${NODE_IP}/24" dev "$AP_IFACE"
    ip route replace 255.255.255.255 dev "$AP_IFACE" 2>/dev/null || true

    if (( NODE_ID > 1 )); then
      ip link set "$MESH_IFACE" up
      # Wait up to 30s for wpa_supplicant to associate with Xenia-1
      for _ in $(seq 1 30); do
        if wpa_cli -i "$MESH_IFACE" status 2>/dev/null | grep -q '^wpa_state=COMPLETED$'; then
          break
        fi
        sleep 1
      done
      ip addr flush dev "$MESH_IFACE"
      ip addr add "10.20.1.${NODE_ID}/24" dev "$MESH_IFACE"
      ip route replace 10.20.1.0/24 dev "$MESH_IFACE" 2>/dev/null || true
    else
      # Node 1 routes for Node 2
      ip route replace 10.20.2.0/24 via 10.20.1.2 dev "$AP_IFACE" 2>/dev/null || true
    fi
    ;;
  stop)
    ip addr flush dev "$AP_IFACE" 2>/dev/null || true
    ip addr flush dev "$MESH_IFACE" 2>/dev/null || true
    ;;
  *) echo "Usage: $0 [start|stop]" >&2; exit 2 ;;
esac
EOF
fi

sed -i -e "s|@NODE_ID@|${NODE_ID}|g" -e "s|@NODE_IP@|${NODE_IP}|g" -e "s|@MESH_IFACE@|${MESH_IFACE}|g" -e "s|@AP_IFACE@|${AP_IFACE}|g" /usr/local/sbin/xenia-mesh-net
chmod 0755 /usr/local/sbin/xenia-mesh-net

# ──────────────────────────────────────────────
#  Systemd Services
# ──────────────────────────────────────────────
cat > /etc/systemd/system/xenia-mesh-net.service <<EOF
[Unit]
Description=Xenia mesh network setup
$( [[ "$BACKHAUL_MODE" == "ibss" || NODE_ID -gt 1 ]] && echo "Requires=wpa_supplicant@${MESH_IFACE}.service" || true )
$( [[ "$BACKHAUL_MODE" == "ibss" || NODE_ID -gt 1 ]] && echo "After=wpa_supplicant@${MESH_IFACE}.service" || true )
Before=hostapd.service dnsmasq.service xenia-node.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/sbin/xenia-mesh-net start
ExecStop=/usr/local/sbin/xenia-mesh-net stop

[Install]
WantedBy=multi-user.target
EOF

mkdir -p /etc/systemd/system/hostapd.service.d /etc/systemd/system/dnsmasq.service.d
cat > /etc/systemd/system/hostapd.service.d/xenia.conf <<EOF
[Unit]
Requires=xenia-mesh-net.service
After=xenia-mesh-net.service

[Service]
Type=simple
ExecStart=
ExecStart=/usr/sbin/hostapd /etc/hostapd/hostapd.conf
EOF
cat > /etc/systemd/system/dnsmasq.service.d/xenia.conf <<EOF
[Unit]
Requires=xenia-mesh-net.service
After=xenia-mesh-net.service
EOF

if [[ -f /etc/dhcpcd.conf ]] && ! grep -q '^denyinterfaces .*\bbr0\b' /etc/dhcpcd.conf; then
  printf '\n# Xenia mesh interfaces\ndenyinterfaces %s %s br0 bat0\n' "$MESH_IFACE" "$AP_IFACE" >> /etc/dhcpcd.conf
fi
install -d -m 0750 -o "$SUDO_USER" -g "$NODE_GROUP" "/var/lib/xenia-node/node${NODE_ID}"

NODE_TLS_ARGS=""
NODE_API_SCHEME="http"
if [[ -f /etc/xenia-node/tls.crt && -f /etc/xenia-node/tls.key ]]; then
  chown "$SUDO_USER:$NODE_GROUP" /etc/xenia-node/tls.crt /etc/xenia-node/tls.key
  chmod 0640 /etc/xenia-node/tls.crt /etc/xenia-node/tls.key
  chmod 0644 /etc/xenia-node/ca.crt
  NODE_API_SCHEME="https"
  NODE_TLS_ARGS="--tls-cert /etc/xenia-node/tls.crt --tls-key /etc/xenia-node/tls.key --peer-ca /etc/xenia-node/ca.crt"
fi

DISCOVERY_IFACE_ARG=""
if [[ "$BACKHAUL_MODE" == "ibss" ]]; then
  DISCOVERY_IFACE_ARG="--discovery-iface br0"
fi

cat > /etc/systemd/system/xenia-node.service <<EOF
[Unit]
Description=Xenia offline communication node
Requires=xenia-mesh-net.service
After=xenia-mesh-net.service network.target

[Service]
Type=simple
User=${SUDO_USER}
Group=${NODE_GROUP}
EnvironmentFile=-/etc/xenia-node/node.env
ExecStart=${NODE_BIN} --listen 0.0.0.0:8080 --node-id node${NODE_ID} --storage-dir /var/lib/xenia-node/node${NODE_ID} --advertise-url ${NODE_API_SCHEME}://${NODE_IP}:8080 ${DISCOVERY_IFACE_ARG} ${NODE_TLS_ARGS}
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/var/lib/xenia-node/node${NODE_ID}

[Install]
WantedBy=multi-user.target
EOF

# NetworkManager remains active for eth0 (CI/CD SSH), but ignores wlan0 and wlan1
mkdir -p /etc/NetworkManager/conf.d
printf "[keyfile]\nunmanaged-devices=interface-name:wlan0;interface-name:wlan1\n" > /etc/NetworkManager/conf.d/99-xenia.conf
nmcli general reload 2>/dev/null || true

systemctl stop wpa_supplicant 2>/dev/null || true
systemctl disable --now hostapd dnsmasq "wpa_supplicant@${AP_IFACE}.service" 2>/dev/null || true
systemctl unmask hostapd "wpa_supplicant@${MESH_IFACE}.service" 2>/dev/null || true
rfkill unblock wlan 2>/dev/null || true
pkill -9 hostapd 2>/dev/null || true
systemctl daemon-reload

if [[ "$BACKHAUL_MODE" == "ibss" || NODE_ID -gt 1 ]]; then
  systemctl restart "wpa_supplicant@${MESH_IFACE}.service"
  systemctl enable "wpa_supplicant@${MESH_IFACE}.service"
fi
systemctl enable xenia-mesh-net.service hostapd.service dnsmasq.service xenia-node.service
systemctl restart xenia-mesh-net.service
systemctl restart hostapd.service dnsmasq.service xenia-node.service

echo "=================================================="
echo "Node ${NODE_ID} is configured (${BACKHAUL_MODE} mode) at ${NODE_IP}."
echo "Connect phones to SSID Xenia-${NODE_ID}."
echo "eth0 is connected for CI/CD management. Wireless interfaces wlan0/wlan1 are configured for Xenia mesh."
echo "=================================================="
