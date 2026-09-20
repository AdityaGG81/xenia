# Infrastructure-Independent Local Communication Network (IILCN)

IILCN is a research project for local communication without Internet, cellular
service, or a centralized server during local operation.  The intended physical
deployment is Android phones connected by Wi-Fi to Raspberry Pi nodes, with the
Pi nodes participating in a multi-hop wireless mesh.

## Development stages

This repository currently implements only **Stage 1: a wired virtual testbed**.
It runs Linux network namespaces linked by virtual Ethernet (veth) pairs.  A
Mininet host represents the network-facing portion of one future Pi node.

This is deliberately not a wireless mesh simulation: it does not model radio
range, interference, Wi-Fi association, channel selection, or battery use.
Its purpose is repeatable testing of addressing, forwarding, service discovery
protocols, and failure-handling logic before hardware is available.

| Stage | Scope | Status |
| --- | --- | --- |
| 1 | Namespace/veth topology and IP forwarding | Complete |
| 2 | Node service, call signalling, message forwarding | Complete |
| 3 | BATMAN-adv in virtual links | Planned |
| 4 | Raspberry Pi wireless mesh and Android client | Planned |

## Virtual testbed

The testbed uses Mininet rather than Docker because Mininet is already installed
and provides exactly the isolation needed: one Linux network namespace per node
and veth pairs for links.  It adds no image lifecycle, container daemon
dependency, or duplicated network abstraction.

Two topologies are supplied:

```text
linear:   n1 ---- n2 ---- n3

diamond:       n2
              /  \
            n1    n3
              \  /
                n4
```

Every link is a separate IPv4 /24 subnet.  Nodes have IP forwarding enabled
inside their namespaces only.  The current test uses static routes, intentionally:
it makes the expected path and recovery action observable.  Static routing is
not the final routing design and does not automatically reconverge after a
failure; the diamond test explicitly changes routes after taking down `n2-n3`.
BATMAN-adv will later replace that experiment with mesh route selection.

Run the checks from this directory:

```bash
./scripts/test-virtual-network.sh linear
./scripts/test-virtual-network.sh diamond
./scripts/test-virtual-network.sh diamond --fail-link n2-n3
```

The command asks `sudo` to create temporary namespaces and veth pairs.  The
topology program always calls Mininet cleanup on normal exit and does not modify
`eno1`, `wlo1`, Docker networks, or persistent host routes.  If a run is
interrupted, use `sudo mn -c`; review its output first, as it cleans stale
Mininet/OVS state.

## Why not BATMAN-adv yet?

The Arch kernel already supplies the `batman_adv` module.  The optional `batctl`
userspace management utility is available in Arch `extra` but is not installed.
No BATMAN-adv configuration has been applied.  We first need repeatable baseline
connectivity and a known link-failure test.  Installing `batctl` later changes
only the addition of that small userspace utility; loading/configuring the module
and creating `bat0` devices will be separate, documented actions.

See [the networking decision record](docs/networking-decisions.md) for the
engineering rationale and deployment limitations.  See the [implementation
journal](docs/project-journal.md) for a chronological explanation of what was
built, why, how it was tested, and what each milestone does not yet prove.

## Initial node service

The dependency-free Go service in [`node/`](node/) provides local health,
presence, user-list, message-buffer, call signalling, and inter-node message
forwarding APIs.  When a peer URL is configured, messages are automatically
forwarded to the remote node for delivery, and call invitations are relayed
across the mesh.

```bash
./scripts/test-node-service.sh
./scripts/test-node-over-network.sh
./scripts/test-call-signalling.sh
```

## Two-phone lab

When the temporary `IILCN-Lab` hotspot is active and both phones are connected:

```bash
./scripts/run-phone-lab.sh
```

This starts the virtual diamond topology with node services on n1 and n3,
and exposes two reverse proxies on the hotspot:

- **Phone A (n1):** `http://10.42.0.1:8081`
- **Phone B (n3):** `http://10.42.0.1:8083`

### Call flow from the phones

1. Phone A: `POST http://10.42.0.1:8081/calls` with `{"from":"alice","to":"bob"}`
2. Phone B: `GET http://10.42.0.1:8083/calls/<call-id>` — see `"state":"ringing"`
3. Phone B: `POST http://10.42.0.1:8083/calls/<call-id>/answer` with `{"accepted":true}`
4. Phone A: `GET http://10.42.0.1:8081/calls/<call-id>` — see `"state":"accepted"`

### Message flow from the phones

1. Phone A: `POST http://10.42.0.1:8081/message` with `{"from":"alice","to":"bob","body":"hello!"}`
2. Phone B: `GET http://10.42.0.1:8083/messages/bob` — see the delivered message

Stop the script with Ctrl+C to remove all virtual interfaces and proxies.
This demonstrates phone-to-virtual-node access, not a real wireless mesh.

## Layout

```text
network/   Mininet topology and routing experiments
node/      Future modular node service
android/   Future Android client
scripts/   Reproducible developer commands
tests/     Automated checks as the project grows
docs/      Architecture and decision records
```
