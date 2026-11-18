# How it works (technical reference)

## Overview

```
 WPA2/WPA3-Enterprise WiFi           Raspberry Pi                           Ethernet side
 (eduroam, company WiFi)                                                    (switch, devices)
                          ┌──────────────────────────────────────────┐
   802.1X: PEAP / TTLS    │ wlan0  profile "eap-bridge-wifi"          │
  ◄──────────────────────►│        DHCP client, IPv4 required         │
                          │              │ NAT (masquerade)           │
                          │              ▼ + IP forwarding            │
                          │ eth0   profile "eap-bridge-lan"           │  192.168.77.0/24
                          │        ipv4.method=shared                 │◄──────────────────►
                          │        dnsmasq: DHCP + DNS on .1          │
                          │                                           │
                          │ eap-bridge (Go, root, systemd)            │
                          │  ├ writes both profiles, calls nmcli      │
                          │  ├ web interface on :80                   │
                          │  └ watchdog, diagnostics, connection test │
                          └──────────────────────────────────────────┘
```

Everything network-related is done by **NetworkManager**, the default network
stack of Raspberry Pi OS since Bookworm. `eap-bridge` only writes connection
profiles, activates them with `nmcli` and watches the result. The Pi therefore
keeps bridging even while the `eap-bridge` service is stopped.

## Why NAT and not a "real" Layer‑2 bridge?

A WiFi client normally sends 802.11 frames in **3-address mode**: the access
point only accepts frames from the client's own MAC address. You cannot put
other devices' MAC addresses behind a WiFi client the way a wired bridge
would. 4-address/WDS mode would need support from the access point, which
enterprise networks do not offer. In addition, 802.1X authenticates **one**
device per login. So the bridge **routes with NAT**: the devices on the
Ethernet side get private addresses, and the network sees all traffic as coming
from the Pi. This is what "internet connection sharing" does on Windows and macOS.

## NetworkManager profiles

Both profiles are written as keyfiles to `/etc/NetworkManager/system-connections/`
(mode 0600, owned by root) and loaded with `nmcli connection load`.
`eap-bridge` never edits them with `nmcli connection modify`; it always rewrites
the whole file.

**`eap-bridge-wifi.nmconnection`** (WiFi uplink)

| Setting | Value | Why |
|---|---|---|
| `wifi.ssid` | byte list (`101;100;…;`) | exact for any network name |
| `wifi-security.key-mgmt` | `wpa-eap` | WPA2/WPA3-Enterprise |
| `802-1x.eap` | `peap` or `ttls` | |
| `802-1x.phase2-auth` / `phase2-autheap` | e.g. `mschapv2`, `pap` | inner method |
| `802-1x.system-ca-certs` + `domain-suffix-match` | "Check the server's domain" | server validated against the system CAs (`/etc/ssl/certs`) |
| `802-1x.ca-cert` | `/etc/eap-bridge/ca.pem` | "Use a CA certificate file" (uploads are converted to PEM) |
| `802-1x.password-flags` | `0` | password stored in the root-only profile |
| `connection.autoconnect-priority` | `100` | preferred over other WiFi profiles (e.g. a setup hotspot) |
| `connection.autoconnect-retries` | `0` | NetworkManager never gives up |
| `wifi.powersave` | `2` (off) | lower latency, fewer drop-outs |
| `ipv4.may-fail` | `false` | the shared side is IPv4, so IPv4 is required |

**`eap-bridge-lan.nmconnection`** (Ethernet side)

| Setting | Value |
|---|---|
| `ipv4.method` | `shared` – NetworkManager starts `dnsmasq` (DHCP + DNS forwarder), adds NAT/forwarding rules (nftables or iptables) and enables `ip_forward` |
| `ipv4.address1` | `lan_address` from the configuration, default `192.168.77.1/24` |
| `ipv6.method` | `link-local` |
| `connection.autoconnect-priority` | `100` |

DHCP leases are read from `/var/lib/NetworkManager/dnsmasq-<interface>.leases`
and shown as "Connected devices".

The default subnet `192.168.77.0/24` was chosen because large organisations
rarely use it. If the WiFi network uses the same range, the status page shows a
warning; change `lan_address` in that case.

## Automatic reconnect

NetworkManager reconnects by itself after short interruptions. After a failed
802.1X login, for example while the organisation's RADIUS server is down,
NetworkManager blocks the profile until new secrets arrive. A watchdog in
`eap-bridge` therefore checks the WiFi every 10 seconds. When it is down and
the network is in range, the watchdog starts a new attempt itself, with a
growing pause between attempts: 2, 5, 10, 15, then 30 minutes. After a **rejected password** it waits 30 minutes straight away, so that
repeated failures do not lock the user's account. **Disconnect** in the web
interface pauses all of this (also across reboots) until the user connects again.

## Diagnostics

When an activation fails, `eap-bridge` reads the NetworkManager and
wpa_supplicant messages written since the attempt started (`journalctl`) and
classifies them:

| Evidence | Diagnosis |
|---|---|
| `CTRL-EVENT-EAP-TLS-CERT-ERROR`, "Domain suffix mismatch", … | server certificate check failed |
| `CTRL-EVENT-EAP-PROPOSED-METHOD method=21/25` differs from the configured method and never selected | wrong EAP method (TLS = 13: certificate login, unsupported) |
| `CTRL-EVENT-EAP-FAILURE`, `reason=AUTH_FAILED` | credentials rejected |
| "could not be found", `CTRL-EVENT-NETWORK-NOT-FOUND` | network not in range |
| "IP configuration could not be reserved" | DHCP failed after a successful login |
| "took too long to authenticate" | timeout (method/certificate/signal) |
| `CTRL-EVENT-EAP-PEER-CERT … CN=…` | the server's name is shown as a hint |

The **connection test** checks, in order: WiFi state, IPv4 address, TCP to
1.1.1.1 / 9.9.9.9 / 8.8.8.8 on port 443, DNS lookup of
`connectivity-check.ubuntu.com`, `http://connectivity-check.ubuntu.com/` (expects
HTTP 204; a redirect or other answer indicates a captive portal; fallback
`http://nmcheck.gnome.org/check_network_status.txt`), Ethernet carrier, sharing
profile active, and DHCP leases.

## Security model

- The service runs as **root** because it writes NetworkManager profiles.
- **Who may open the web interface:** clients whose address is in the Ethernet
  subnet (a TCP handshake from that range can only complete over the Ethernet
  port, because replies are routed there), IPv6 link-local clients on the Ethernet
  interface, and localhost. From the WiFi side, access is only allowed while the
  WiFi is **not** connected through the enterprise profile, and only from the same
  local network. This makes setup over a phone hotspot possible, and the page is
  never exposed to strangers on eduroam. Configure this with `uplink_access`.
- **CSRF:** a per-browser token (double-submit cookie) on every form, plus
  `Sec-Fetch-Site`/`Origin` checks.
- **DNS rebinding:** only IP addresses, `localhost`, `<hostname>` and
  `<hostname>.local` (plus `allowed_hosts`) are accepted as `Host`.
- **Headers:** strict Content-Security-Policy (no inline scripts or styles),
  `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: same-origin`, `no-store`.
- **Optional admin password:** PBKDF2-HMAC-SHA256 (120,000 iterations, random salt),
  stateless HMAC session cookies (30 days; changing the password logs out all
  sessions), and a growing delay after 5 wrong attempts. A file
  `eap-bridge-reset-password` on the boot partition removes the password at the next start.
- **The WiFi password** is never sent back to the browser. "Leave empty to keep"
  reads it from NetworkManager on the Pi. It is stored only in the root-only profile.
- The web interface uses plain HTTP. The Ethernet side is a local cable network.

## Files

| Path | Content |
|---|---|
| `/usr/local/bin/eap-bridge` | the program |
| `/etc/systemd/system/eap-bridge.service` | systemd unit |
| `/etc/eap-bridge/config.json` | configuration (0600) |
| `/etc/eap-bridge/ca.pem` | uploaded CA certificate (optional) |
| `/etc/NetworkManager/system-connections/eap-bridge-*.nmconnection` | NetworkManager profiles (0600) |

## Configuration reference (`/etc/eap-bridge/config.json`)

Restart the service after editing: `sudo systemctl restart eap-bridge`.

| Key | Default | Meaning |
|---|---|---|
| `listen` | `":80"` | address of the web interface |
| `wifi_interface` | `""` (auto) | WiFi interface, e.g. `wlan0` |
| `lan_interface` | `""` (auto, prefers `eth0`) | Ethernet interface for sharing |
| `manage_lan` | `true` | create/maintain the sharing profile |
| `lan_address` | `"192.168.77.1/24"` | address and subnet of the Ethernet side |
| `uplink_access` | `"auto"` | web access from the WiFi side: `auto`, `always` (same local network only) or `never` |
| `allowed_hosts` | `[]` | extra host names for the web interface |
| `admin_password_hash`, `session_secret`, `lan_uuid`, `wifi`, `paused` | – | managed by the program |

## Command line

```text
eap-bridge serve [--config FILE] [--listen ADDR] [--demo]
eap-bridge status
eap-bridge reset-password
eap-bridge version
```

## Requirements

- Raspberry Pi OS **Bookworm** or newer (NetworkManager). Other Debian-based systems with
  NetworkManager work as well.
- `dnsmasq-base` and `nftables` or `iptables`. Raspberry Pi OS includes them, and the installer adds them if missing.
- Any Raspberry Pi with WiFi and Ethernet (or a USB Ethernet adapter). Builds:
  `arm64` (64-bit OS), `armv6` (32-bit OS, all models), `amd64`.
