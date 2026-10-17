#!/usr/bin/env bash
# Restores NetworkManager to regain connection to the home Wi-Fi.

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root: sudo $0"
  exit 1
fi

echo "Restoring NetworkManager..."
systemctl disable --now hostapd dnsmasq wpa_supplicant@wlan1 wpa_supplicant@wlan0 2>/dev/null || true
systemctl stop hostapd 2>/dev/null || true
systemctl stop dnsmasq 2>/dev/null || true
systemctl disable hostapd 2>/dev/null || true
systemctl disable dnsmasq 2>/dev/null || true

systemctl unmask NetworkManager || true
systemctl enable NetworkManager
systemctl start NetworkManager

echo "NetworkManager restored! The Raspberry Pi should now reconnect to your home Wi-Fi."
