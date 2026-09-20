#!/usr/bin/env bash
# Fixes Docker's DROP policy on the FORWARD chain blocking NetworkManager hotspot internet.
# Run this if the hotspot devices show "Limited connectivity" or "No internet".

sudo iptables -I DOCKER-USER -i wlo1 -j ACCEPT
sudo iptables -I DOCKER-USER -o wlo1 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
echo "Hotspot routing fixed."
