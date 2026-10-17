# Raspberry Pi BATMAN-adv node setup

Xenia uses a peer mesh, not a parent/child Wi-Fi tree. Each node has a wireless
backhaul running in IBSS mode under BATMAN-adv. A second, separate radio provides
a regular Wi-Fi access point for phones. The mesh layer carries the node LAN
and the phone access points, so every node and phone can reach peers over the
same Layer 2 network.

## Hardware and OS

- Raspberry Pi OS 64-bit and a Raspberry Pi supported by that image.
- The built-in/internal `wlan0` (or `XENIA_AP_IFACE`) with AP mode support for
  phones.
- A **separate external Wi-Fi adapter** `wlan1` (or `XENIA_MESH_IFACE`) with
  IBSS/ad-hoc support for the mesh. The external adapter does not need AP mode;
  the internal adapter does not need IBSS mode. A single-radio setup is rejected
  because the current design needs both modes at the same time.
- Kernel `batman-adv` module and the `iw`, `batctl`, `wpa_supplicant`,
  `hostapd`, and `dnsmasq` tools.

On Raspberry Pi OS, install the userspace tools if they are not already present:

```bash
sudo apt update
sudo apt install -y iw batctl wpasupplicant hostapd dnsmasq
```

The installer checks that the external mesh adapter advertises IBSS, the
internal phone adapter advertises AP mode, that they are separate radios, and
that the kernel can load `batman_adv`. Check the actual adapter names and modes
on both Pis before setup:

```bash
iw dev
iw list
```

The external adapter must support IBSS even though it does not need AP mode.
Adapter mode support depends on the driver.

## Deploy and set up both Pis

From the development computer, run one command with each Pi's SSH login and
reachable address. This builds the Raspberry Pi binary, creates the local TLS
certificates if needed, and copies each Pi its matching certificate and files:

```bash
./scripts/deploy-pi-pair.sh <user>@<pi-1-address> <user>@<pi-2-address>
```

Then run the printed command on each Pi from its local console or a wired SSH
session:

```bash
sudo ~/install-xenia-pi.sh 1   # Pi 1
sudo ~/install-xenia-pi.sh 2   # Pi 2
```

Setup disables NetworkManager and changes Wi-Fi configuration, so a Wi-Fi SSH
session will disconnect. The helper generates and provisions the shared node API
token and phone AP password, so there are no credential prompts on the Pis. It
prints the generated AP password at the end; use it for the `Xenia-1` and
`Xenia-2` phone networks. Keep the local `deployment/node-secrets.env` private.
The mesh backhaul itself is open IBSS, which avoids WPA/IBSS driver compatibility
issues; the phone access points remain WPA2-protected. The helper installs the
correct TLS files before starting setup and never copies the private CA key to a
Pi.

Use a unique node ID from 1 to 254 on every node. Every Pi must use the same
mesh channel and mesh ID; the backhaul is open IBSS and has no Wi-Fi password.
Set `XENIA_MESH_CHANNEL`, `XENIA_MESH_ID`, `XENIA_MESH_IFACE`, or
`XENIA_AP_IFACE` before setup to override the defaults. The generated API token
is stored in `/etc/xenia-node/node.env` and matches across the nodes. The node
service broadcasts discovery on `br0`, the bridge
that carries BATMAN-adv traffic, and advertises `10.20.<node-id>.1` to peers.
If your interface names differ from `wlan0`/`wlan1`, pass the overrides through
sudo, for example:

```bash
sudo env XENIA_MESH_IFACE=wlx001122334455 XENIA_AP_IFACE=wlan0 \
  ~/install-xenia-pi.sh 1
```

The mesh addresses are `10.20.<node-id>.1`. The AP advertises as `Xenia-<id>`;
phones receive an address from a node-specific range with a shared `/16` mask,
allowing direct phone-to-phone traffic across the bridged mesh.

## Enable secure audio calls

The deployment helper configures HTTPS on both nodes. Import
`deployment/tls/ca.crt` from the development computer onto both phones and trust
it, then open `https://10.20.1.1:8080` or `https://10.20.2.1:8080`. Keep
`deployment/tls/ca.key` private on the development computer. Phone browser
microphone access requires a trusted HTTPS certificate.

## Check mesh formation

On each Pi, run:

```bash
sudo ~/mesh-status.sh
sudo batctl meshif bat0 neighbors
sudo batctl meshif bat0 originators
```

Each visible neighbor should appear in `neighbors`; `originators` should show
other mesh nodes, including those reached over multiple radio hops. Check
`http://10.20.<node-id>.1:8080/health` from a phone or another node, or use
`https://` when TLS is installed. Verify the peer count rises to 1 on each Pi.
The response lists the discovered peer
and its last discovery time. Also check `journalctl -u xenia-node.service -f`
for a `discovered peer` line and successful sync messages. Check
`http://10.20.<node-id>.1:8080/users` (or `https://` with TLS) after
connecting one phone to each AP.

The phone APs are `Xenia-1` and `Xenia-2`; use the generated AP password printed
by the deployment command. The
PWA should show the other connected user. Calls carry audio directly between
phones; the Go service relays call setup signaling. No STUN/TURN service is
configured, so both phones must have a direct path across the bridged mesh.

The service starts at boot through systemd. To restore the original host network
configuration, run `sudo ~/restore-network.sh`; the script restores the one-time
backups it created and enables NetworkManager again.

## Current limits

The Wi-Fi IBSS link uses one fixed 2.4 GHz channel and all mesh nodes share its
passphrase. The setup bridges phone AP clients across the mesh; it does not
provide Internet access. Radio compatibility, interference, throughput,
mobility, and recovery after a real radio failure still need validation on the
target Pis and adapters.
