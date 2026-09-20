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
