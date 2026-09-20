#!/usr/bin/env python3
"""Two-phone lab: both n1 and n3 are accessible from the hotspot.

Phone A -> wlo1 (10.42.0.1:8081) -> proxy -> n1 (172.20.1.2:8080)
Phone B -> wlo1 (10.42.0.1:8083) -> proxy -> n3 (172.20.3.2:8080)

n1 and n3 are configured as each other's signalling/messaging peer,
so calls and messages traverse the virtual mesh: n1 <-> n2 <-> n3.

All veth and proxy state created here is removed on Ctrl+C or normal exit.
"""

import argparse
import signal
import subprocess
import sys
import subprocess
import sys
import time
import traceback

from mininet.link import Link
from mininet.net import Mininet
from mininet.node import Host

from topology import add_link, command, configure_diamond, configure_forwarding, configure_link

# Host-to-n1 access link.
HOST_N1_IP = "172.20.1.1/30"
NODE_N1_IP = "172.20.1.2/30"
N1_SERVICE = "172.20.1.2:8080"

# Host-to-n3 access link.
HOST_N3_IP = "172.20.3.1/30"
NODE_N3_IP = "172.20.3.2/30"
N3_SERVICE = "172.20.3.2:8080"

# Mesh-internal service addresses for peer-url configuration.
N1_MESH_ADDR = "10.10.12.1:8080"
N3_MESH_ADDR = "10.10.23.2:8080"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--node-binary", required=True, help="path to the built iilcn-node binary")
    parser.add_argument("--proxy-binary", required=True, help="path to the built IILCN access proxy")
    args = parser.parse_args()

    net = Mininet(controller=None, build=False, autoSetMacs=True)
    nodes = {name: net.addHost(name, cls=Host) for name in ("n1", "n2", "n3", "n4")}
    gateway = net.addHost("gateway", cls=Host, inNamespace=False)
    mesh_links = {name: add_link(net, name) for name in ("n1-n2", "n2-n3", "n3-n4", "n4-n1")}

    # Access links: host <-> n1, host <-> n3
    access_n1 = net.addLink(nodes["n1"], gateway, cls=Link, intfName1="n1-access0", intfName2="iilcn-gw0")
    access_n3 = net.addLink(nodes["n3"], gateway, cls=Link, intfName1="n3-access0", intfName2="iilcn-gw1")

    proxies = []
    try:
        net.build()
        for name, link in mesh_links.items():
            configure_link(link, name)

        access_n1.intf1.setIP(NODE_N1_IP)
        access_n1.intf2.setIP(HOST_N1_IP)
        access_n3.intf1.setIP(NODE_N3_IP)
        access_n3.intf2.setIP(HOST_N3_IP)

        configure_forwarding(nodes)
        configure_diamond(nodes)

        # Start node services with mesh-internal peer URLs.
        nodes["n1"].cmd(
            f"rm -rf /tmp/iilcn-n1-data && mkdir -p /tmp/iilcn-n1-data && "
            f"{args.node_binary} --node-id n1 --listen 0.0.0.0:8080 "
            f"--peer-url http://{N3_MESH_ADDR} --storage-dir /tmp/iilcn-n1-data >/tmp/iilcn-n1.log 2>&1 &"
        )
        nodes["n3"].cmd(
            f"rm -rf /tmp/iilcn-n3-data && mkdir -p /tmp/iilcn-n3-data && "
            f"{args.node_binary} --node-id n3 --listen 0.0.0.0:8080 "
            f"--peer-url http://{N1_MESH_ADDR} --storage-dir /tmp/iilcn-n3-data >/tmp/iilcn-n3.log 2>&1 &"
        )

        # Wait for both services to become healthy.
        for label, service_url in [("n1", N1_SERVICE), ("n3", N3_SERVICE)]:
            for _ in range(15):
                result = gateway.cmd(f"curl --fail --silent --max-time 1 http://{service_url}/health")
                if f'"node_id":"{label}"' in result:
                    break
                time.sleep(0.2)
            else:
                log = nodes[label].cmd(f"cat /tmp/iilcn-{label}.log")
                raise RuntimeError(f"{label} service did not start: {log}")

        # Start two reverse proxies: port 8081 -> n1, port 8083 -> n3.
        # Bind to 0.0.0.0 so they are accessible from the host's normal network (e.g. home Wi-Fi).
        proxy_n1 = subprocess.Popen((
            args.proxy_binary,
            "--listen", "0.0.0.0:8081",
            "--upstream", f"http://{N1_SERVICE}",
        ))
        proxy_n3 = subprocess.Popen((
            args.proxy_binary,
            "--listen", "0.0.0.0:8083",
            "--upstream", f"http://{N3_SERVICE}",
        ))
        proxies.extend([proxy_n1, proxy_n3])
        time.sleep(0.3)

        for p in proxies:
            if p.poll() is not None:
                raise RuntimeError("A phone access proxy did not start")

        print("=" * 60)
        print("  IILCN Two-Phone Lab is running!")
        print("=" * 60)
        print()
        print("  You can now access the lab from ANY network interface.")
        print("  If you are connected to the hotspot, use 10.42.0.1:")
        print("    Phone A (n1): http://10.42.0.1:8081")
        print("    Phone B (n3): http://10.42.0.1:8083")
        print()
        print("  If your phones are on your normal home Wi-Fi, use your PC's IP (e.g. 192.168.1.5):")
        print("    Phone A (n1): http://192.168.1.5:8081")
        print("    Phone B (n3): http://192.168.1.5:8083")
        print()
        print("  Call flow:")
        print("    1. Phone A: POST http://<IP>:8081/calls")
        print('       Body: {"from":"alice","to":"bob"}')
        print("    2. Phone B: GET http://<IP>:8083/calls/<call-id>")
        print("    3. Phone B: POST http://<IP>:8083/calls/<call-id>/answer")
        print('       Body: {"accepted":true}')
        print("    4. Phone A: GET http://<IP>:8081/calls/<call-id>")
        print()
        print("  Message flow:")
        print("    1. Phone A: POST http://<IP>:8081/message")
        print('       Body: {"from":"alice","to":"bob","body":"hello!"}')
        print("    2. Phone B: GET http://<IP>:8083/messages/bob")
        print()
        print("  Press Ctrl+C to stop and clean up.")
        print("=" * 60)
        signal.pause()
    finally:
        for p in proxies:
            p.terminate()
            try:
                p.wait(timeout=3)
            except subprocess.TimeoutExpired:
                p.kill()
        net.stop()


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, lambda _signal, _frame: (_ for _ in ()).throw(KeyboardInterrupt()))
    try:
        main()
    except KeyboardInterrupt:
        pass
    except Exception as error:
        traceback.print_exc()
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
