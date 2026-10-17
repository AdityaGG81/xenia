# Xenia node

The Go node serves the bundled PWA, handles local device registration and
WebSocket sessions, discovers mesh peers over UDP port `9999`, and forwards
presence, messages, acknowledgments, and call signalling over HTTP. The Pi
provisioning target uses BATMAN-adv for Layer 2 forwarding over an IBSS radio;
the app service runs on the bridged mesh address.

## Build and run

From the repository root:

```bash
./scripts/build-node.sh --host
./bin/xenia-node --node-id node1 --listen 0.0.0.0:8080 \
  --advertise-url http://10.20.1.1:8080
```

The binary defaults to persistent storage under the operating system's user
config directory. Set `--storage-dir` to choose a different writable location.
Use a stable node ID and an address other nodes can reach. UDP discovery
broadcasts on every IPv4 interface by default. On a Pi mesh deployment, the
installer sets `--advertise-url` to the node's bridge address and
`--discovery-iface br0` so discovery stays on the BATMAN-adv network.
The Raspberry Pi network setup is documented in
[`../docs/node-setup-guide.md`](../docs/node-setup-guide.md).

Build a 64-bit Raspberry Pi binary with `./scripts/build-node.sh --pi`; it is
written to `bin/pi/xenia-node`. This target is Linux ARM64 and requires a 64-bit
Raspberry Pi OS. It is not compatible with 32-bit Pi OS.

## Security and calls

Set the same `XENIA_MESH_TOKEN` on every node to authenticate internal mesh API
requests. Without it, internal endpoints accept requests from any reachable
host. Keep nodes on a trusted local network. HTTPS can be enabled with both
`--tls-cert` and `--tls-key`. Use `--peer-ca` to trust a private CA for HTTPS
between nodes; the Pi installer sets the HTTPS advertise URL and this CA path
when it finds the required files. Phones also need to trust the CA before the
browser will grant microphone access. The browser exchanges WebRTC offers,
answers, and ICE candidates through the Go service; audio travels directly
between phones. No STUN/TURN service is configured, so both phones need a
direct network path across the bridged mesh.

## API

- `GET /health` reports node ID, connected peers/clients, and storage health.
- `POST /register` accepts `{"username":"alice"}` and returns a stable device
  UUID. One device per username is supported on each node.
- `GET /users` returns users currently known online.
- `GET /ws?device_id=…` upgrades to the client WebSocket.

Client WebSocket events include `heartbeat`, `send_message`, `message_ack`,
`call_offer`, `call_answer`, `call_hangup`, `call_reject`, and `ice_candidate`.
The node sends `presence_sync`, `user_online`, `user_offline`, `message_history`,
`new_message`, `message_sent`, `message_status`, and `error` events.

Internal endpoints under `/internal/` exchange presence and forward messages,
delivery acknowledgments, and call signalling. Messages are persisted as JSONL
and the log is compacted after it grows beyond 32 MiB. Presence and active calls
are in memory; pending messages are retried while peers are reachable.
