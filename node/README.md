# Node service

The first Xenia node service is a dependency-free Go HTTP process.  It is
intentionally small: it provides a stable application boundary while network
experiments evolve from veth links to BATMAN-adv and Raspberry Pi Wi-Fi.

## Run

```bash
cd node
go run -buildvcs=false ./cmd/xenia-node --node-id node1 --listen 127.0.0.1:8080
```

To enable inter-node communication, configure a peer:

```bash
go run -buildvcs=false ./cmd/xenia-node --node-id n1 --listen 10.10.12.1:8080 --peer-url http://10.10.23.2:8080
```

## API

`GET /health` returns the node identifier and service status.

`GET /users` returns known users ordered by `user_id`.

`POST /presence` records local presence.  Required JSON fields: `user_id` and
`status` (for example `online` or `away`).

```json
{"user_id":"alice","status":"online"}
```

`POST /message` validates and buffers a message locally.  When a peer URL is
configured, the message is automatically forwarded to the peer node for
delivery.  Required JSON fields: `from`, `to`, and `body`.

```json
{"from":"alice","to":"bob","body":"Hello from Xenia"}
```

Response state is `"forwarded"` if the message was relayed, or `"buffered"` if
no peer is configured or forwarding failed.

`GET /messages/{user}` retrieves all messages involving a user (sent or
received).

### Call signalling

`POST /calls` initiates a call.  The invitation is relayed to the configured
peer.  Required JSON fields: `from` and `to`.

```json
{"from":"alice","to":"bob"}
```

`GET /calls/{id}` returns the current state of a call.

`POST /calls/{id}/answer` accepts or rejects a call.  The decision is relayed
back to the originating node.

```json
{"accepted":true}
```

### Internal endpoints

These are used for inter-node communication and are not intended for direct
client use:

- `POST /internal/message` — receive a forwarded message
- `POST /internal/calls` — receive a relayed call invitation
- `POST /internal/calls/{id}/state` — receive a call state update

Presence and messages are currently in memory, so they disappear when the
process stops.  Run unit tests with `go test ./...` from this directory.

