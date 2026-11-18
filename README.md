# EAP Bridge for Raspberry Pi

Turn a Raspberry Pi into a small box that logs in to a **WPA2-Enterprise
WiFi** with username and password (802.1X/EAP, for example **eduroam** or a
company WiFi) and **shares that internet connection on its Ethernet port**.

Use it to connect devices that cannot log in to such networks themselves:
desktop PCs without WiFi, game consoles, smart TVs, streaming boxes, printers,
and, with a small access point, WiFi-only devices like a Nintendo Switch or a
Chromecast.

```
eduroam / company WiFi ))) Raspberry Pi ──── Switch ──┬── PC
         (username + password)                         ├── TV / console
                                                       └── access point ((( WiFi-only devices
```

## Features

- **Simple web interface** at <http://192.168.77.1>:
  - choose the WiFi network from a list of nearby networks (hidden networks work too)
  - enter username and password; PEAP or TTLS with all common inner methods
  - three server-certificate options: check the domain (the default), upload a CA file, or no check
  - **live connection progress** and **plain-language error explanations**, such as
    "the network rejected the login" or "the network uses TTLS"
  - **step-by-step connection test**: WiFi → IP → internet → DNS → web → cable → sharing
  - list of connected devices, WiFi country, optional admin password, restart/shut down
- **Internet sharing on Ethernet**: DHCP, DNS and NAT, all through NetworkManager
- **Reliable unattended operation**: reconnects automatically, with careful back-off so a
  wrong password does not lock your account
- **Safe defaults**: the web interface is never reachable from the enterprise WiFi, and
  forms are protected against CSRF and DNS rebinding
- **One small program**: a static Go binary with no dependencies, so it is easy to
  install for beginners. Both an online installer and an offline installer are included.

## Documentation

| Guide | For |
|---|---|
| **[Installation guide](docs/INSTALL.md)** | step-by-step setup, written for beginners |
| **[Connecting your devices](docs/CONNECTING-DEVICES.md)** | using a switch, devices that only have WiFi, what to expect |
| **[Troubleshooting](docs/TROUBLESHOOTING.md)** | common problems and solutions |
| **[How it works](docs/HOW-IT-WORKS.md)** | technical reference, configuration, security model |

> [!NOTE]
> The Raspberry Pi's built-in WiFi cannot join networks set to **WPA2/WPA3-Enterprise**
> (mixed mode) or **WPA3-Enterprise**. The network must offer **WPA2-Enterprise**.
> See [known limitations](docs/HOW-IT-WORKS.md#known-limitations).

> [!IMPORTANT]
> Many organisations do not allow sharing their network or connecting your own network
> equipment. Check the rules, and only connect your own devices. Everything that goes
> through the bridge counts as traffic from your account.

## Quick start (if you know your way around a Pi)

On Raspberry Pi OS Lite (Bookworm or newer) with temporary internet:

```bash
curl -fsSL https://github.com/pkramme/rpi-eap-bridge/releases/latest/download/install.sh | sudo bash
```

Then connect a computer to the Pi's Ethernet port and open <http://192.168.77.1>.

## Try the web interface without a Raspberry Pi

```bash
go run ./cmd/eap-bridge serve --demo
```

Open <http://127.0.0.1:8080>. NetworkManager is simulated in this mode. The password
`wrong` shows a failed login, and the network "CorpNet" only accepts PEAP.

## Development

Requirements: Go 1.22 or newer.

```bash
make test      # unit tests
make demo      # web interface with simulated network
make pi        # bin/eap-bridge-linux-arm64 for a 64-bit Raspberry Pi OS
make release   # all release files into dist/
```

To test on a Pi without a release, copy the binary and the installer to the Pi
and run:

```bash
sudo bash install.sh --binary ./eap-bridge-linux-arm64
```

Project layout:

```
cmd/eap-bridge/      command line (serve, status, reset-password)
internal/bridge/     connect, status, watchdog, connection test
internal/nm/         nmcli wrapper and NetworkManager keyfile writer
internal/diag/       internet probes and log analysis
internal/web/        web interface (templates and static files are embedded)
internal/demo/       simulated NetworkManager for --demo and tests
internal/system/     DHCP leases, journal, WiFi country, power
scripts/             install.sh, uninstall.sh, package-release.sh
```
