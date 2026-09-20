#!/usr/bin/env python3
"""Reproducible Xenia namespace/veth routing testbed.

Run through scripts/test-virtual-network.sh so Mininet has required privileges.
"""

import argparse
import sys
import time

from mininet.link import Link
from mininet.log import setLogLevel
from mininet.net import Mininet
from mininet.node import Host


LINKS = {
    "n1-n2": ("n1", "n2", "10.10.12.1/24", "10.10.12.2/24"),
    "n2-n3": ("n2", "n3", "10.10.23.1/24", "10.10.23.2/24"),
    "n3-n4": ("n3", "n4", "10.10.34.1/24", "10.10.34.2/24"),
    "n4-n1": ("n4", "n1", "10.10.41.1/24", "10.10.41.2/24"),
}

MESH_ADDRS = {
    "n1": "10.20.0.1/24",
    "n2": "10.20.0.2/24",
    "n3": "10.20.0.3/24",
    "n4": "10.20.0.4/24",
}


def command(node, cmd):
    """Run a node command and fail loudly if it fails."""
    output = node.cmd(f"{cmd}; printf '__Xenia_EXIT:%s__\\n' $?")
    body, marker = output.rsplit("__Xenia_EXIT:", 1)
    if int(marker.strip().rstrip("_")) != 0:
        raise RuntimeError(f"{node.name}: {cmd}\n{body}")
    return body


def add_link(net, name):
    left, right, _, _ = LINKS[name]
    link = net.addLink(net.get(left), net.get(right), cls=Link)
    return link


def configure_link(link, name):
    _, _, left_ip, right_ip = LINKS[name]
    link.intf1.setIP(left_ip)
    link.intf2.setIP(right_ip)


def configure_forwarding(nodes):
    for node in nodes.values():
        command(node, "sysctl -q -w net.ipv4.ip_forward=1")


def configure_linear(nodes):
    command(nodes["n1"], "ip route replace 10.10.23.0/24 via 10.10.12.2")
    command(nodes["n3"], "ip route replace 10.10.12.0/24 via 10.10.23.1")


def configure_diamond(nodes):
    # Primary path n1 -> n2 -> n3; n4 is reserved as the alternate path.
    command(nodes["n1"], "ip route replace 10.10.23.0/24 via 10.10.12.2")
    command(nodes["n3"], "ip route replace 10.10.12.0/24 via 10.10.23.1")


def configure_diamond_recovery(nodes):
    """Explicit static-route recovery after the n2-n3 link is disabled."""
    command(nodes["n1"], "ip route replace 10.10.23.0/24 via 10.10.41.1")
    command(nodes["n3"], "ip route replace 10.10.12.0/24 via 10.10.34.2")
    command(nodes["n4"], "ip route replace 10.10.23.0/24 via 10.10.34.1")
    command(nodes["n4"], "ip route replace 10.10.12.0/24 via 10.10.41.2")


def configure_mesh(nodes):
    for node_name, node in nodes.items():
        # Bring up all point-to-point interfaces and enslave them to BATMAN-adv
        for intf in node.intfs.values():
            if intf.name != "lo":
                command(node, f"ip link set up dev {intf.name}")
                command(node, f"ip link set promisc on dev {intf.name}")
                command(node, f"batctl if add {intf.name}")
        
        # Disable advanced features that break in isolated veth namespaces
        command(node, "batctl mm 0")  # Multicast optimizations
        command(node, "batctl dat 0") # Distributed ARP table
        command(node, "batctl bl 0")  # Bridge loop avoidance
        
        # Bring up the mesh interface and assign its unified IP address
        command(node, "ip link set up dev bat0")
        command(node, f"ip addr add {MESH_ADDRS[node_name]} dev bat0")


def verify_mesh(nodes, title):
    print(f"\n== {title} ==")
    # Give BATMAN-adv time to discover neighbors and calculate routes
    for _ in range(15):
        try:
            result = command(nodes["n1"], "ping -c 1 -W 1 10.20.0.3")
            if " 0% packet loss" in result:
                print("PASS: n1 reaches n3 across the bat0 mesh interface.")
                return
        except RuntimeError:
            pass
        time.sleep(1)
    raise RuntimeError("BATMAN-adv mesh connectivity check failed after 15 seconds")


def verify(nodes, title):
    print(f"\n== {title} ==")
    # Bind to n1's stable test address rather than whichever egress link wins.
    result = command(nodes["n1"], "ping -n -I 10.10.12.1 -c 2 -W 1 10.10.23.2")
    print(result, end="")
    if " 0% packet loss" not in result:
        raise RuntimeError("end-to-end connectivity check failed")
    print("PASS: n1 reaches n3 across the intended multi-hop path.")


def verify_node_api(nodes, binary, title):
    """Start a real node service in n3 and call it from n1 across the topology."""
    print(f"\n== {title} ==")
    nodes["n3"].cmd(f"rm -rf /tmp/xenia-n3-data && mkdir -p /tmp/xenia-n3-data && {binary} --node-id n3 --listen 10.10.23.2:8080 --storage-dir /tmp/xenia-n3-data >/tmp/xenia-n3.log 2>&1 &")
    health_url = "http://10.10.23.2:8080/health"
    health = ""
    for _ in range(10):
        health = nodes["n1"].cmd(f"curl --fail --silent --max-time 1 --interface 10.10.12.1 {health_url}")
        if '"status":"ok"' in health:
            break
        time.sleep(0.1)
    if '"node_id":"n3"' not in health:
        raise RuntimeError(f"n1 could not reach n3 node service: {nodes['n3'].cmd('cat /tmp/xenia-n3.log')}")

    presence = nodes["n1"].cmd(
        "curl --fail --silent --max-time 1 --interface 10.10.12.1 -X POST http://10.10.23.2:8080/presence "
        "-H 'Content-Type: application/json' -d '{\"user_id\":\"alice\",\"status\":\"online\"}'"
    )
    users = nodes["n1"].cmd("curl --fail --silent --max-time 1 --interface 10.10.12.1 http://10.10.23.2:8080/users")
    message = nodes["n1"].cmd(
        "curl --fail --silent --max-time 1 --interface 10.10.12.1 -X POST http://10.10.23.2:8080/message "
        "-H 'Content-Type: application/json' -d '{\"from\":\"alice\",\"to\":\"bob\",\"body\":\"mesh test\"}'"
    )
    if '"user_id":"alice"' not in presence or '"user_id":"alice"' not in users or '"state":"buffered"' not in message:
        raise RuntimeError("node API responses did not contain expected presence/message state")
    print("PASS: n1 reached n3's node API and completed health, presence, user, and message requests.")


def verify_node_health(nodes, title):
    """Verify that the n3 service remains reachable after a route change."""
    print(f"\n== {title} ==")
    health = command(nodes["n1"], "curl --fail --silent --max-time 1 --interface 10.10.12.1 http://10.10.23.2:8080/health")
    if '"node_id":"n3"' not in health:
        raise RuntimeError("n3 node service is not reachable after route recovery")
    print("PASS: n1 still reaches n3's node API via the alternate path.")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("topology", choices=("linear", "diamond", "mesh"))
    parser.add_argument("--fail-link", choices=("n2-n3",))
    parser.add_argument("--node-binary", help="path to an Xenia node binary for HTTP integration testing")
    args = parser.parse_args()
    if args.fail_link and args.topology not in ("diamond", "mesh"):
        parser.error("--fail-link is supported only by diamond and mesh topologies")

    net = Mininet(controller=None, build=False, autoSetMacs=True)
    active = ("n1-n2", "n2-n3") if args.topology == "linear" else tuple(LINKS)
    node_names = ("n1", "n2", "n3") if args.topology == "linear" else ("n1", "n2", "n3", "n4")
    nodes = {name: net.addHost(name, cls=Host) for name in node_names}
    created = {name: add_link(net, name) for name in active}

    try:
        net.build()
        if args.topology != "mesh":
            # Apply per-link IP addresses and routing for static topologies
            for name, link in created.items():
                configure_link(link, name)
            configure_forwarding(nodes)
            
        if args.topology == "linear":
            configure_linear(nodes)
            verify(nodes, "linear topology")
            if args.node_binary:
                verify_node_api(nodes, args.node_binary, "linear node API")
        elif args.topology == "diamond":
            configure_diamond(nodes)
            verify(nodes, "diamond primary path")
            if args.node_binary:
                verify_node_api(nodes, args.node_binary, "diamond primary node API")
            if args.fail_link:
                link = created[args.fail_link]
                left = net.get(link.intf1.node.name)
                right = net.get(link.intf2.node.name)
                command(left, f"iptables -I INPUT -i {link.intf1.name} -j DROP")
                command(left, f"iptables -I OUTPUT -o {link.intf1.name} -j DROP")
                command(right, f"iptables -I INPUT -i {link.intf2.name} -j DROP")
                command(right, f"iptables -I OUTPUT -o {link.intf2.name} -j DROP")
                print("Disabled virtual link n2-n3 by dropping traffic.")
                
                configure_diamond_recovery(nodes)
                verify(nodes, "diamond alternate path after n2-n3 failure")
                if args.node_binary:
                    verify_node_health(nodes, "diamond node API after route recovery")
        elif args.topology == "mesh":
            # Bring up the links without assigning point-to-point IP addresses
            for name, link in created.items():
                link.intf1.ifconfig("up")
                link.intf2.ifconfig("up")
            # No static ip_forwarding needed; BATMAN-adv routes at Layer 2
            configure_mesh(nodes)
            verify_mesh(nodes, "BATMAN-adv mesh primary path")
            if args.fail_link:
                link = created[args.fail_link]
                left = net.get(link.intf1.node.name)
                right = net.get(link.intf2.node.name)
                command(left, f"iptables -I INPUT -i {link.intf1.name} -j DROP")
                command(left, f"iptables -I OUTPUT -o {link.intf1.name} -j DROP")
                command(right, f"iptables -I INPUT -i {link.intf2.name} -j DROP")
                command(right, f"iptables -I OUTPUT -o {link.intf2.name} -j DROP")
                print("Disabled virtual link n2-n3 by dropping traffic.")
                
                # We do NOT run a recovery function! BATMAN-adv heals automatically.
                verify_mesh(nodes, "BATMAN-adv mesh alternate path after n2-n3 failure")
    finally:
        net.stop()


if __name__ == "__main__":
    setLogLevel("warning")
    try:
        main()
    except Exception as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
