# Xenia Node Setup Guide

> [!WARNING]  
> **Living Document:** The Xenia project is actively evolving. As new features (like true store-and-carry gossip, USB Wi-Fi access points for mobile clients, or database persistence) are added to the system, the setup steps and dependencies below will change. **Developers must update this document** whenever new dependencies or setup steps are introduced.

This guide outlines the exact steps to take a brand new Raspberry Pi (with a fresh installation of Raspberry Pi OS / Debian) and provision it as a fully functional node in the Xenia mesh network.

---

## 1. Initial OS Preparation

1. **Flash OS:** Use Raspberry Pi Imager to flash **Raspberry Pi OS (64-bit)** to an SD card. 
2. **Enable SSH:** Before writing the image, use the advanced settings in the Imager to:
   - Enable SSH (with password authentication or SSH keys).
   - Set a hostname (e.g., `xenia-node-3`).
   - Configure a default username and password.
   - Connect it to your home/lab Wi-Fi network initially so you can SSH into it.
3. **Boot:** Insert the SD card, power on the Pi, and find its IP address on your local network.

## 2. Compile and Transfer Software (From Host PC)

The Xenia node service is written in Go and must be cross-compiled for the Raspberry Pi's ARM64 architecture before transferring it.

On your **Development PC**, navigate to the Xenia repository and run:

```bash
# Compile the node binary for ARM64
cd node
GOOS=linux GOARCH=arm64 go build -o ../bin/pi/xenia-node ./cmd/xenia-node/
cd ..

# Transfer the binary and setup scripts to the new Pi's home directory
# Replace <user> and <pi-ip> with the actual credentials
scp bin/pi/xenia-node scripts/setup-mesh-pi.sh scripts/start-node.sh <user>@<pi-ip>:~/

# Make the scripts executable on the Pi
ssh <user>@<pi-ip> "chmod +x ~/xenia-node ~/setup-mesh-pi.sh ~/start-node.sh"
```

## 3. Install Dependencies (On the Raspberry Pi)

SSH into the new Raspberry Pi. Currently, the only system dependency required is `batctl`, the userspace configuration tool for the `batman-adv` kernel module.

```bash
sudo apt update
sudo apt install -y batctl
```

## 4. Join the Mesh Network

The `setup-mesh-pi.sh` script automates the process of disconnecting the Pi from your local Wi-Fi, configuring the internal Wi-Fi chip (`wlan0`) into Ad-Hoc (IBSS) mode, and layering the BATMAN-adv mesh interface (`bat0`) on top.

Run the setup script, passing it a unique Node ID number (e.g., `3` for the third node):

```bash
sudo ~/setup-mesh-pi.sh 3
```

> [!NOTE]
> Running this script will immediately disconnect the Pi from your home Wi-Fi network as it joins the isolated `10.20.0.x` mesh network. If you are connected via Wi-Fi SSH, your session will drop. You will need a keyboard/monitor or an Ethernet connection to continue.

## 5. Start the Node Service

Once the Pi is on the mesh network, start the Xenia application service. This service handles offline message buffering, presence broadcasting, and call signalling.

Run the start script, passing it the same Node ID number:

```bash
~/start-node.sh 3
```

The node is now fully operational! It will automatically peer with any other Xenia nodes in physical Wi-Fi range and begin routing traffic via BATMAN-adv.

---

## Future Hardware Enhancements (Pending)
As of the current architecture, the single built-in Wi-Fi chip (`wlan0`) is completely dedicated to the mesh backend. Because standard unrooted Android phones cannot connect to Ad-Hoc networks, future setups will require plugging in a **USB Wi-Fi Adapter (e.g., Ralink RT5370)** to act as a standard Access Point (`wlan1`) for mobile phones to connect to the node. This document will be updated once that bridging infrastructure is finalized.
