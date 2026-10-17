# Xenia

Xenia is an offline-first local communication prototype. Phones connect to a
nearby Raspberry Pi node over Wi-Fi. The target network uses BATMAN-adv over an
IBSS wireless backhaul, with a separate access point radio for phones. Nodes
share presence, forward chat messages, and relay WebRTC call signalling. Audio
travels directly between phones over the bridged mesh.

## What is implemented

- A Go node service that serves the React PWA and a WebSocket API.
- Stable device registration and local message persistence.
- UDP peer discovery, presence synchronization, bounded multi-hop message
  forwarding, and delivery acknowledgments.
- A Pi setup script for a BATMAN-adv mesh with a bridged phone access point.
- WebRTC offer/answer/ICE signalling. Calls require HTTPS for microphone access
  and a direct network path between phones; STUN/TURN is not configured.
- A build script for the development host and 64-bit ARM Linux Raspberry Pi.

This is a prototype, not a finished secure messenger. Presence is temporary,
message logs are retained on each node, mesh traffic uses HTTP unless the local
network provides additional protection, and every node in a deployment should
use the same `XENIA_MESH_TOKEN` secret.

## Build the Raspberry Pi binary

Install Go, Node.js, and npm on the build machine, then run:

```bash
./scripts/build-node.sh --pi
```

The script builds the PWA, embeds it in the Go application, and creates
`bin/pi/xenia-node` for Linux ARM64 (Raspberry Pi OS 64-bit). For two Pis, use
the pair deployment helper, which also provisions TLS and generated secrets:

```bash
./scripts/deploy-pi-pair.sh <user>@<pi-1-address> <user>@<pi-2-address>
```

By default, each Pi uses the external `wlan1` adapter for IBSS mesh backhaul and
the built-in `wlan0` adapter for the phone AP. The external adapter does not need
AP mode, but it must support IBSS. The mesh backhaul is open IBSS; the phone APs
remain WPA2-protected with a generated password printed by the deploy helper.
On each Pi, run `sudo ~/install-xenia-pi.sh 1` or `sudo
~/install-xenia-pi.sh 2` from a local console or wired session. The helper
provisions matching API credentials and installs systemd services.
Because setup disables NetworkManager and changes Wi-Fi configuration, run it
from a local console or wired session. See
[`docs/node-setup-guide.md`](docs/node-setup-guide.md) for prerequisites and
mesh diagnostics. The current mesh script is not yet validated on Pi hardware.

## Development

```bash
cd client && npm ci && npm run dev
```

By default, Vite proxies API and WebSocket requests to `127.0.0.1:8080`. Set
`XENIA_NODE_URL` if the development node runs elsewhere. Build the host binary
and embedded UI with `./scripts/build-node.sh --host`.

See [`node/README.md`](node/README.md) for API details.
