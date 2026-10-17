#!/usr/bin/env bash
# Remove Xenia mesh services and restore one-time network configuration backups.
set -euo pipefail
if (( EUID != 0 )); then
  echo "Run as root: sudo $0" >&2
  exit 1
fi

systemctl disable --now xenia-node.service xenia-mesh-net.service hostapd.service dnsmasq.service \
  wpa_supplicant@wlan0.service wpa_supplicant@wlan1.service 2>/dev/null || true
rm -f /etc/systemd/system/xenia-node.service /etc/systemd/system/xenia-mesh-net.service
rm -f /etc/systemd/system/hostapd.service.d/xenia.conf /etc/systemd/system/dnsmasq.service.d/xenia.conf
rm -f /usr/local/sbin/xenia-mesh-net

restore_once() {
  local original="$1"
  if [[ -e "${original}.xenia-original" ]]; then
    cp -a "${original}.xenia-original" "$original"
    echo "Restored $original"
  fi
}
restore_once /etc/dhcpcd.conf
restore_once /etc/dnsmasq.conf
restore_once /etc/hostapd/hostapd.conf
for backup in /etc/wpa_supplicant/*.xenia-original; do
  [[ -e "$backup" ]] || continue
  original=${backup%.xenia-original}
  cp -a "$backup" "$original"
  echo "Restored $original"
done

systemctl daemon-reload
systemctl unmask NetworkManager 2>/dev/null || true
systemctl enable --now NetworkManager
systemctl restart dhcpcd 2>/dev/null || true
echo "Network configuration restored. Backups remain in place."
