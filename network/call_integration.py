#!/usr/bin/env python3
"""Integration test: call signalling and message forwarding over the virtual mesh.

Starts a diamond topology with node services on n1 and n3, each configured
as the other's signalling/messaging peer.  Exercises:
  1. Call invite from n1, verify arrival at n3
  2. Call acceptance from n3, verify propagation back to n1
  3. Message from n1 forwarded to n3, verify delivery and retrieval
"""

import argparse
import json
import sys
import time

from mininet.log import setLogLevel
from mininet.net import Mininet
from mininet.node import Host

from topology import (
    LINKS,
    add_link,
    command,
    configure_diamond,
    configure_forwarding,
    configure_link,
)

N1_ADDR = "10.10.12.1"
N3_ADDR = "10.10.23.2"
N1_SERVICE = f"{N1_ADDR}:8080"
N3_SERVICE = f"{N3_ADDR}:8080"


def wait_for_service(node, url, interface, label, retries=15):
    for _ in range(retries):
        result = node.cmd(
            f"curl --fail --silent --max-time 1 --interface {interface} {url}"
        )
        if '"status":"ok"' in result:
            return
        time.sleep(0.2)
    raise RuntimeError(f"{label} did not become healthy at {url}")


def curl_json(node, method, url, interface, data=None):
    parts = [
        "curl", "--fail", "--silent", "--max-time", "2",
        "--interface", interface,
    ]
    if method == "POST":
        parts += ["-X", "POST", "-H", "'Content-Type: application/json'"]
        if data:
            parts += ["-d", f"'{json.dumps(data)}'"]
    parts.append(url)
    raw = node.cmd(" ".join(parts))
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        raise RuntimeError(f"Invalid JSON from {url}: {raw!r}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--node-binary", required=True)
    args = parser.parse_args()

    net = Mininet(controller=None, build=False, autoSetMacs=True)
    nodes = {name: net.addHost(name, cls=Host) for name in ("n1", "n2", "n3", "n4")}
    mesh_links = {name: add_link(net, name) for name in LINKS}

    try:
        net.build()
        for name, link in mesh_links.items():
            configure_link(link, name)
        configure_forwarding(nodes)
        configure_diamond(nodes)

        # Start n1 and n3 services with each other as the signalling/messaging peer.
        n1_peer = f"http://{N3_SERVICE}"
        n3_peer = f"http://{N1_SERVICE}"
        nodes["n1"].cmd(
            f"rm -rf /tmp/xenia-n1-data && mkdir -p /tmp/xenia-n1-data && "
            f"{args.node_binary} --node-id n1 --listen {N1_SERVICE} "
            f"--peer-url {n1_peer} --storage-dir /tmp/xenia-n1-data >/tmp/xenia-n1.log 2>&1 &"
        )
        nodes["n3"].cmd(
            f"rm -rf /tmp/xenia-n3-data && mkdir -p /tmp/xenia-n3-data && "
            f"{args.node_binary} --node-id n3 --listen {N3_SERVICE} "
            f"--peer-url {n3_peer} --storage-dir /tmp/xenia-n3-data >/tmp/xenia-n3.log 2>&1 &"
        )

        wait_for_service(nodes["n1"], f"http://{N1_SERVICE}/health", N1_ADDR, "n1")
        wait_for_service(nodes["n3"], f"http://{N3_SERVICE}/health", N3_ADDR, "n3")
        print("Both node services are healthy.")

        # --- Call signalling test ---
        print("\n== Call signalling over mesh ==")

        # 1. Alice on n1 calls Bob on n3.
        call = curl_json(
            nodes["n1"], "POST", f"http://{N1_SERVICE}/calls", N1_ADDR,
            {"from": "alice", "to": "bob"},
        )
        call_id = call["id"]
        assert call["state"] == "ringing", f"Expected ringing, got {call['state']}"
        print(f"  n1: Call created: {call_id} (state={call['state']})")

        # 2. Verify the call arrived at n3.
        remote_call = curl_json(
            nodes["n3"], "GET", f"http://{N3_SERVICE}/calls/{call_id}", N3_ADDR,
        )
        assert remote_call["state"] == "ringing", f"Expected ringing at n3, got {remote_call['state']}"
        print(f"  n3: Call received: {call_id} (state={remote_call['state']})")

        # 3. Bob accepts the call on n3.
        answer = curl_json(
            nodes["n3"], "POST", f"http://{N3_SERVICE}/calls/{call_id}/answer", N3_ADDR,
            {"accepted": True},
        )
        assert answer["state"] == "accepted", f"Expected accepted, got {answer['state']}"
        print(f"  n3: Call answered: {call_id} (state={answer['state']})")

        # 4. Verify the acceptance propagated back to n1.
        updated = curl_json(
            nodes["n1"], "GET", f"http://{N1_SERVICE}/calls/{call_id}", N1_ADDR,
        )
        assert updated["state"] == "accepted", f"Expected accepted at n1, got {updated['state']}"
        print(f"  n1: Call state updated: {call_id} (state={updated['state']})")
        print("PASS: Call signalling completed across the mesh.")

        # --- Message forwarding test ---
        print("\n== Message forwarding over mesh ==")

        # 1. Alice sends a message from n1 to Bob on n3.
        msg = curl_json(
            nodes["n1"], "POST", f"http://{N1_SERVICE}/message", N1_ADDR,
            {"from": "alice", "to": "bob", "body": "hello over mesh"},
        )
        assert msg["state"] == "forwarded", f"Expected forwarded, got {msg['state']}"
        msg_id = msg["id"]
        print(f"  n1: Message sent: {msg_id} (state={msg['state']})")

        # 2. Bob retrieves his messages on n3.
        inbox = curl_json(
            nodes["n3"], "GET", f"http://{N3_SERVICE}/messages/bob", N3_ADDR,
        )
        delivered = [m for m in inbox["messages"] if m["id"] == msg_id]
        assert len(delivered) == 1, f"Expected 1 delivered message, got {len(delivered)}"
        assert delivered[0]["state"] == "delivered", f"Expected delivered, got {delivered[0]['state']}"
        assert delivered[0]["body"] == "hello over mesh"
        print(f"  n3: Message delivered: {msg_id} (state={delivered[0]['state']})")
        print("PASS: Message forwarding completed across the mesh.")

    finally:
        net.stop()


if __name__ == "__main__":
    setLogLevel("warning")
    try:
        main()
    except Exception as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
