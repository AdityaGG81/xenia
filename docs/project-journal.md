# IILCN implementation journal

This is the running record of what has been implemented, why the decision was
made, how it was verified, and what remains outside its scope.  Update this file
as each milestone is completed; it is intended to support the final-year project
report as well as day-to-day development.

## Project objective

IILCN aims to provide local communication between Android users through nearby
Raspberry Pi nodes without Internet, cellular service, or a central server during
local operation.  The eventual system needs discovery, presence, messaging,
offline buffering, signalling, and multi-hop forwarding.  Voice calling and
complex authentication are intentionally deferred.

## 2026-09-04 — Host capability assessment

**Implemented / observed.** Inspected the Arch Linux development host without
changing its configuration.  It has kernel `7.1.5-arch1-2`, Mininet `2.3.1b4`,
Open vSwitch `3.7.1`, Docker `29.6.2`, network namespaces, and the veth kernel
module.  The kernel includes `batman_adv` version `2025.5`; Arch provides the
optional `batctl` utility, but it is not installed.  The laptop has a Realtek
RTL8852AE Wi-Fi adapter, although its wireless interface was down during the
inspection.

**Why.** The project must reuse installed capabilities and avoid premature
package installation or host-network changes.  The assessment showed that
Mininet plus namespaces/veth links is sufficient for the first milestone.

**Limit.** The inspection did not claim mesh-mode support for the laptop Wi-Fi
adapter, because the `iw` userspace tool was not installed and real wireless
mesh testing is not yet in scope.

## 2026-09-04 — Virtual wired testbed

**Implemented.** `network/topology.py` creates temporary Mininet hosts and
direct veth links for two layouts: `n1-n2-n3` and the redundant four-node
diamond.  Each link has an independent IPv4 /24 subnet.  IP forwarding is
enabled only inside the temporary node namespaces.  The test confirms that n1
reaches n3 through n2; in the diamond it disables `n2-n3`, installs explicit
alternate static routes through n4, and confirms n1 can still reach n3.

**Why.** Direct veth links expose individual hops clearly, need no extra
software, and provide a controlled baseline before routing protocols are
introduced.  Static routes make the intended route and failure-recovery action
auditable.

**Verification.** The linear, diamond-primary, and diamond-failure commands all
passed with two ICMP replies per test.

**Limit.** This is a virtual wired network, not a wireless mesh.  Static routes
do not discover peers or autonomously converge after failure.  The manual route
replacement is an experiment and will later be replaced by dynamic mesh routing.

## 2026-09-04 — Initial node service

**Implemented.** A standard-library-only Go service in `node/` exposes
`GET /health`, `GET /users`, `POST /presence`, and `POST /message`.  Presence is
stored in memory.  Messages are validated, assigned a node-local ID, and marked
`buffered` in memory.  The service package owns the state, while the command
package supplies the HTTP listener; this keeps business state separate from a
specific network transport.

**Why.** It creates a minimal, testable application boundary now without tying
the service to Mininet, BATMAN-adv, Raspberry Pi interfaces, or Android.  Go was
selected because it was already installed and the required service needs no
external library.

**Verification.** Unit tests cover successful health/presence/user/message
flows and malformed requests.  A local HTTP smoke test exercised every endpoint.

**Limit.** There is no persistence, identity verification, encryption, peer
discovery, forwarding, delivery acknowledgment, or real inter-node message
transport.  A successful `POST /message` means only local buffering.

## 2026-09-04 — Node service over the virtual network

**Implemented.** `scripts/test-node-over-network.sh` builds the node binary,
starts it inside the n3 Mininet namespace, and calls its API from n1 over the
diamond topology.  It performs health, presence, user-list, and message-buffer
requests before the failure.  It then disables `n2-n3`, activates the static
alternate route via n4, and confirms that n1 still reaches n3's health endpoint.

**Why.** ICMP proves only IP reachability.  This test proves that the actual node
process can bind to a node interface and exchange application-layer HTTP traffic
over both the primary and alternate virtual paths.

**Limit.** Only n3 hosts a service in this first integration test.  API calls are
client-to-server requests, not distributed messaging between independent node
buffers.  That is the next application-network milestone.

## 2026-09-04 — Smartphone access-network preparation

**Implemented.** Created the temporary `IILCN-Lab` WPA2 hotspot on the laptop's
otherwise unused `wlo1` interface using NetworkManager.  Its hotspot interface
address is `10.42.0.1/24`.  NetworkManager successfully started AP mode with the
RTL8852AE/`rtw89_8852ae` radio.  Activation initially failed because the
NetworkManager shared-IPv4 feature requires `dnsmasq`; after confirming it was
absent, installed the single Arch `extra/dnsmasq` package (version `2.93-1`,
approximately 1.1 MiB installed) and activated the hotspot successfully.

**Why.** One laptop Wi-Fi radio cannot currently provide physically separate
access networks for two phone-edge nodes, but one shared access network lets a
real Android phone reach an assigned virtual edge node.  This is the smallest
hardware-free step toward an end-to-end phone-to-node prototype.

**Verification.** NetworkManager reports `wlo1` as connected to `IILCN-Lab` and
assigns it `10.42.0.1/24`; Ethernet (`eno1`) remains independently connected.

**Limit.** This is only an access-network preparation step.  No virtual edge
interface or phone-to-node forwarding has been configured yet, and one shared
AP does not emulate separate physical radio edges or a real wireless mesh.  The
NetworkManager shared mode may offer upstream connectivity through the host;
before an offline-only demonstration, explicitly restrict phone traffic to the
local IILCN routes/services.

## 2026-09-04 — Phone-to-virtual-node access lab

**Implemented.** Added `network/phone_lab.py` and `scripts/run-phone-lab.sh`.
The runner starts the four-node virtual diamond, attaches n1 to a temporary
host-side veth link, starts the real node service in n1, and starts a dedicated
temporary HTTP access proxy.  The proxy maps the hotspot gateway's TCP port 8081
to n1's service at `172.20.1.2:8080`.  A phone on `IILCN-Lab` can therefore call
`http://10.42.0.1:8081/health` and receive n1's actual API response.

**Why.** The phone cannot directly address an IP that exists only inside a
Mininet namespace.  The temporary host-to-n1 link and narrowly scoped HTTP
proxy provide a clear access boundary without exposing the virtual topology to
the physical LAN or adding general packet-forwarding rules.

**Cleanup and limit.** The lab runner terminates its proxy and removes Mininet
interfaces on Ctrl+C or normal exit.  It currently exposes only n1 and verifies
phone-to-node HTTP, not phone-to-phone calls or separate physical Wi-Fi edges.

**Physical-phone verification.** A real Android phone joined `IILCN-Lab`,
received the hotspot address range, was configured to remain connected despite
Android's expected “no Internet” warning, and successfully loaded
`http://10.42.0.1:8081/health`.  The response identified virtual node n1.  This
proves phone-to-virtual-node HTTP reachability over a local Wi-Fi access network
without relying on Internet or cellular connectivity.

## 2026-09-04 — Inter-node message forwarding

**Implemented.** Extended the Go node service so that `POST /message` now
forwards the message to the configured peer node via `POST /internal/message`.
The originating node marks the message as `forwarded`; the receiving node stores
it as `delivered`.  Added `GET /messages/{user}` so a user can retrieve all
messages they have sent or received.

**Why.** The previous milestone only buffered messages locally.  For two phones
to communicate through the mesh, messages must traverse the node topology:
Phone A → n1 (buffer + forward) → n3 (deliver) → Phone B retrieves.

**Verification.** New unit tests `TestMessageForwardingBetweenPeers` and
`TestMessageWithoutPeerStaysBuffered` pass.  The integration test
`scripts/test-call-signalling.sh` verifies forwarding over the virtual diamond.

**Limit.** Forwarding targets only one explicit peer.  There is no multi-hop
message routing, store-and-forward retry queue, delivery acknowledgment, or
encryption.  Messages are in memory only.

## 2026-09-04 — Call signalling over the virtual mesh

**Implemented.** Created `network/call_integration.py` and
`scripts/test-call-signalling.sh`.  The test starts the diamond topology with
node services on both n1 and n3, each configured with `--peer-url` pointing to
the other.  It exercises the full call flow: Alice on n1 initiates a call to
Bob, the invitation arrives at n3 as `ringing`, Bob accepts on n3, and the
`accepted` state propagates back to n1.

**Why.** The call signalling endpoints existed but had only been tested
in-process via unit tests.  This proves the signalling HTTP traffic actually
traverses the virtual multi-hop path (n1 → n2 → n3 and back).

**Verification.** The integration test passes: call creation, relay, acceptance,
and state propagation all succeed over the mesh.

**Limit.** Only two-party signalling is tested.  There is no audio relay,
timeout, or cleanup of stale calls.

## 2026-09-04 — Two-phone lab

**Implemented.** Rewrote `network/phone_lab.py` to support two phones.  The
lab now starts node services on both n1 and n3 (each as the other's peer),
creates two host-side access links (host ↔ n1, host ↔ n3), and starts two
reverse proxies:

- Port 8081 on `10.42.0.1` → n1 (Phone A)
- Port 8083 on `10.42.0.1` → n3 (Phone B)

**Why.** The previous phone lab only exposed n1.  To demonstrate a complete
phone-to-phone call or message flow through the mesh, both edge nodes must be
accessible from separate phones.

**Verification.** Automated smoke test confirmed: both proxies return correct
health responses, a call invitation from n1 reaches n3, acceptance propagates
back, a message forwarded from n1 arrives at n3 as `delivered`, and
`GET /messages/bob` returns the message.

**Limit.** Both phones share the same physical Wi-Fi AP, so this does not
demonstrate real wireless separation.  The reverse proxies handle HTTP only;
real-time audio or WebSocket-based features would need a different transport.

## 2026-09-04 — Durable offline message storage

**Implemented.** Added the `--storage-dir` flag to the node binary and updated the Go `service` package to persist messages. The service now writes an append-only JSONL (JSON Lines) file containing message state changes. On startup, it reads this file to reconstruct its in-memory message list and sequence counters. The testing scripts (`phone_lab.py`, `call_integration.py`, and `test-node-over-network.sh`) were updated to isolate state in temporary directories per run.

**Why.** When operating without central servers or continuous internet, nodes will inevitably disconnect or restart. Disk persistence guarantees that buffered messages are not lost if a node powers off before it can forward them. JSONL was chosen over a database like SQLite because it requires no CGO or external libraries, keeping the node binary maximally portable for the final Raspberry Pi deployment.

**Verification.** Added a new integration test script `scripts/test-offline-storage.sh`. The script starts a node, posts a message, terminates the node process, restarts it pointing to the same storage directory, and successfully retrieves the original message.

**Limit.** The append-only log grows indefinitely because there is no compaction or garbage collection yet. Call signalling states and presence are not persisted because they are considered ephemeral.

## 2026-09-04 — BATMAN-adv Mesh Routing

**Implemented.** Transitioned the virtual network from static IP routing to dynamic, Layer-2 mesh routing using the B.A.T.M.A.N. advanced kernel module (`batman-adv`). Added a new `mesh` topology mode to `network/topology.py` that configures `bat0` interfaces for each node and assigns them a unified mesh IP subnet (`10.20.0.0/24`). To support Mininet's isolated `veth` namespaces, we disabled some of BATMAN-adv's advanced optimizations (DAT, Multicast Mode, Bridge Loop Avoidance) and enabled promiscuous mode, allowing raw Ethernet encapsulation to pass through cleanly.

**Why.** Static routes are rigid and require manual intervention when links go down, making them useless for a mobile mesh network. BATMAN-adv operates at Layer 2, automatically discovering peers, measuring link quality, and instantly rerouting traffic when a node moves out of range or a link fails.

**Verification.** Created `scripts/test-mesh-network.sh`. The test establishes a 4-node diamond topology, verifies end-to-end connectivity, and then simulates a link failure using `iptables DROP`. The test proves that BATMAN-adv instantly recalculates the route and seamlessly switches to the alternate path with zero packet loss or manual intervention.

**Limit.** The virtual network still uses perfect point-to-point `veth` links. Real-world Wi-Fi involves shared mediums, hidden node problems, and fluctuating signal qualities, which BATMAN-adv will have to handle natively on the Raspberry Pis.

## 2026-09-04 — Mobile PWA Web Client

**Implemented.** Scrapped the idea of a native Android APK and built a **Progressive Web App (PWA)** using React and Vite instead. The client is a responsive, mobile-first interface featuring a premium dark-mode aesthetic. It includes a Connection screen, real-time Presence tracking in a sidebar, a chat interface, and incoming call overlays. The Node API was also expanded: updated the `/users` endpoint and added `/calls` polling and `/internal/presence` mesh-broadcasting. Finally, configured IP Forwarding (`MASQUERADE`) on the host to provide the Linux hotspot with internet, bypassing Android's captive portal disconnect issue.

**Why.** Compiling and distributing Android APKs creates too much overhead for rapid prototyping. A PWA acts identically to a native app when accessed via a phone browser, allowing us to test cross-device functionality instantly over the local Wi-Fi lab.

**Verification.** Successfully accessed the PWA from a physical Android device connecting to the host's Vite dev server (`0.0.0.0:5173`). Demonstrated live messaging and call signaling between the physical phone and the PC, traversing the virtual Mininet backend proxies.

**Limit.** The Web App currently relies on HTTP polling (`setInterval`) every 2 seconds to fetch calls and messages, which is inefficient. A future iteration should transition the Go API to WebSocket or Server-Sent Events (SSE) for true real-time, low-latency push updates.

## 2026-09-13 — Raspberry Pi Hardware Deployment

**Implemented.** Moved the IILCN mesh network from the simulated Mininet environment to physical hardware using two Raspberry Pis. Cross-compiled the Go `iilcn-node` binary for the ARM64 architecture (`GOOS=linux GOARCH=arm64`). Created deployment scripts (`deploy-to-pi.sh` and `setup-mesh-pi.sh`) to automate provisioning. Configured the built-in Broadcom Wi-Fi chips on both Pis to disconnect from managed networks and join an isolated **IBSS (ad-hoc) cell** on 2.4 GHz Channel 1. Finally, layered the `batman-adv` kernel module over the ad-hoc interfaces (`wlan0`), creating a dynamic Layer 2 mesh network (`bat0`) with IPs `10.20.0.1` and `10.20.0.2`.

**Why.** Mininet veth links and virtual namespaces are excellent for rapid iteration, but they cannot simulate real-world radio characteristics, driver quirks, or the physical constraints of an isolated edge environment. Deploying to real hardware proves that the architecture is viable for a decentralized field deployment without external infrastructure.

**Verification.** Verified that `batctl n` successfully discovered the peer Pi over the ad-hoc Wi-Fi link. Started the `iilcn-node` service on both Pis and executed `curl` commands directly against the node APIs. Demonstrated that a JSON payload sent to Pi 1 was instantly routed over the airwaves via the BATMAN protocol to Pi 2, where it was successfully buffered and served via HTTP.

**Limit.** The built-in Wi-Fi chip (`wlan0`) is currently consumed entirely by the ad-hoc mesh. Because standard unrooted Android phones refuse to connect to ad-hoc networks, an end-user cannot natively connect their phone directly to a Pi node yet. A physical deployment requires a second Wi-Fi interface (e.g., a USB Wi-Fi adapter) to broadcast a standard Wi-Fi Access Point (`wlan1`) for client phones to connect to.

## Planned sequence

1. ~~Run one node service per virtual node and implement inter-node
   forwarding/delivery between node buffers.~~ Done.
2. ~~Add durable offline message storage and delivery tests.~~ Done.
3. ~~Install `batctl` only after reviewing the exact package change, then test
   BATMAN-adv on the existing virtual links.~~ Done.
4. ~~Move the validated service and routing design to Raspberry Pi hardware and
   conduct separate real Wi-Fi mesh tests.~~ Done.
5. ~~Develop the Android client after the node API and local transport are stable.~~ **Replaced with Mobile PWA Web Client.** Done.
