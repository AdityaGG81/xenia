# Networking decisions

## ADR-001: Start with Mininet namespaces and veth links

**Decision.** Represent each Xenia node as a Mininet host (a Linux network
namespace), connected by veth pairs.  Use direct host-to-host links rather than
an Open vSwitch data plane for the initial topologies.

**What it does.** A namespace has its own interfaces, addresses, routes, and
process table view.  A veth pair behaves like a point-to-point Ethernet cable:
one end is placed in each node namespace.

**Why.** Mininet, Open vSwitch, network namespaces, and veth support are already
available on the development host.  Direct links make each hop unambiguous and
avoid adding switches that do not correspond to the final mesh-node design.

**Alternatives considered.**

* Docker containers: available, but add container images and daemon-specific
  networking without improving this first routing experiment.
* Raw `ip netns` shell scripts: viable, but Mininet gives lifecycle management,
  topology construction, and interactive inspection with less boilerplate.
* Open vSwitch bridges: useful for LAN/switch experiments, but not necessary for
  explicit point-to-point node links.

**Limitations.** This tests wired virtual links only; it cannot validate wireless
mesh performance, Wi-Fi driver behavior, or radio propagation.

**Relation to Raspberry Pi deployment.** The namespace boundary maps cleanly to
the Pi node's network/service boundary.  Link and route configuration will later
be replaced by physical wireless interfaces and BATMAN-adv, while the node
service protocol remains independent of the transport.

## ADR-002: Use static routes as the baseline

**Decision.** Configure forwarding and explicit static routes for the baseline.

**Why.** This proves that node-to-node traffic traverses an intermediate node and
makes a link-failure recovery experiment deterministic.  It also supplies a
simple control case for a later BATMAN-adv comparison.

**Alternatives considered.** OSPF/Babel routing daemons and BATMAN-adv.  They
would obscure whether basic interface, forwarding, and application behavior is
correct.  BATMAN-adv remains the intended mesh layer after this baseline.

**Limitations.** Static routes do not discover peers or automatically repair a
path.  The redundant-topology test explicitly installs the alternate route after
a link is disabled; this is a test of topology and reachability, not a claim of
automatic failover.

**Relation to Raspberry Pi deployment.** Real Pi nodes will need dynamic mesh
routing, route convergence testing, and persistence rules.  Those are outside
this baseline.

## ADR-003: Use BATMAN-adv over an IBSS backhaul

**Decision.** The Raspberry Pi deployment uses BATMAN-adv at Layer 2 over a
WPA2 protected 2.4 GHz IBSS backhaul. A separate Wi-Fi radio runs a phone AP;
the AP and `bat0` join a Linux bridge. Each node has a unique `10.20.<id>.1`
address and DHCP clients receive addresses from a node-specific range in the
shared mesh prefix.

**Why.** Nodes can peer with any radio neighbor and BATMAN-adv selects paths
across multiple hops, instead of requiring a parent node ID or manually
configured routes. Bridging the phone APs to the mesh gives phones direct
Layer 2 reachability for WebRTC media as well as access to their local node.

**Hardware constraint.** Concurrent mesh backhaul and phone AP operation uses
two distinct Wi-Fi radios. By default, the external `wlan1` adapter provides
IBSS for mesh and the built-in `wlan0` provides AP mode for phones. The external
adapter does not need AP support. The installer checks each radio only for its
assigned mode before changing networking. A compatible driver and radio are
required; software cannot enable a mode the adapter does not provide.

**Limitations.** The setup scripts and address plan are implemented but have
not been run on Raspberry Pi hardware. Channel selection, client DHCP across
multiple bridged nodes, mesh encryption interoperability, route convergence,
radio recovery, and cross-node calls remain hardware validation items.
