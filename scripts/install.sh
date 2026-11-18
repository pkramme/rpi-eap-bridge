#!/usr/bin/env bash
# EAP Bridge installer for Raspberry Pi OS (Bookworm, Trixie or newer).
#
# Online:   curl -fsSL https://github.com/pkramme/rpi-eap-bridge/releases/latest/download/install.sh | sudo bash
# Offline:  sudo bash /boot/firmware/eap-bridge/install.sh   (from the release ZIP copied to the SD card)
#
# Options:
#   --binary FILE    install this eap-bridge binary instead of downloading one
#   --country CC     WiFi country code (e.g. DE, GB, US) if none is set yet
#   --version TAG    release to download (default: latest)
#   --yes            do not ask questions
set -euo pipefail

REPO="${EAP_BRIDGE_REPO:-pkramme/rpi-eap-bridge}"
VERSION="${EAP_BRIDGE_VERSION:-latest}"
BIN_DST=/usr/local/bin/eap-bridge
CONF_DIR=/etc/eap-bridge
CONF_FILE="$CONF_DIR/config.json"
UNIT_FILE=/etc/systemd/system/eap-bridge.service

BINARY=""
COUNTRY=""
ASSUME_YES=0

say()  { printf '\n\033[1;32m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '\n\033[1;33mWARNING:\033[0m %s\n' "$*" >&2; }
die()  { printf '\n\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --binary)  BINARY="${2:?--binary needs a file}"; shift 2 ;;
    --country) COUNTRY="${2:?--country needs a code}"; shift 2 ;;
    --version) VERSION="${2:?--version needs a tag}"; shift 2 ;;
    --yes|-y)  ASSUME_YES=1; shift ;;
    -h|--help) sed -n '2,13p' "$0" 2>/dev/null || true; exit 0 ;;
    *) die "Unknown option: $1" ;;
  esac
done

# Questions are read from the terminal, so they also work with "curl ... | sudo bash".
ask() {
  local prompt="$1" answer=""
  if [ "$ASSUME_YES" -eq 1 ] || ! { : </dev/tty; } 2>/dev/null; then
    echo ""
    return
  fi
  read -r -p "$prompt" answer </dev/tty || true
  echo "$answer"
}

[ "$(id -u)" -eq 0 ] || die "Please run the installer with sudo, for example: sudo bash install.sh"

SCRIPT_DIR=""
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fi

say "Checking the system"
command -v nmcli >/dev/null 2>&1 \
  || die "NetworkManager (nmcli) was not found. Please use Raspberry Pi OS Bookworm (2023) or newer."
systemctl is-active --quiet NetworkManager \
  || die "NetworkManager is not running. Please use a current Raspberry Pi OS image."

case "$(uname -m)" in
  aarch64|arm64)        ARCH=arm64 ;;
  armv6l|armv7l|armv8l) ARCH=armv6 ;;
  x86_64|amd64)         ARCH=amd64 ;;
  *) die "Unsupported processor: $(uname -m)" ;;
esac
info "Processor type: $ARCH"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ---------------------------------------------------------------- program file
say "Getting the eap-bridge program"
if [ -n "$BINARY" ]; then
  [ -f "$BINARY" ] || die "File not found: $BINARY"
  SRC="$BINARY"
  info "Using $SRC"
elif [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/bin/eap-bridge-linux-$ARCH" ]; then
  SRC="$SCRIPT_DIR/bin/eap-bridge-linux-$ARCH"
  info "Using $SRC (offline installation)"
  if [ -f "$SCRIPT_DIR/bin/SHA256SUMS" ]; then
    (cd "$SCRIPT_DIR/bin" && grep " eap-bridge-linux-$ARCH\$" SHA256SUMS | sha256sum -c --quiet -) \
      || die "The program file is damaged (checksum mismatch). Please copy the folder to the SD card again."
  fi
else
  if [ "$VERSION" = latest ]; then
    BASE="https://github.com/$REPO/releases/latest/download"
  else
    BASE="https://github.com/$REPO/releases/download/$VERSION"
  fi
  command -v curl >/dev/null 2>&1 || die "curl is missing. Install it with: sudo apt install curl"
  info "Downloading from $BASE"
  curl -fL --retry 3 --progress-bar -o "$TMP/eap-bridge-linux-$ARCH" "$BASE/eap-bridge-linux-$ARCH" \
    || die "Download failed. Is the Raspberry Pi connected to the internet?"
  if curl -fsSL --retry 3 -o "$TMP/SHA256SUMS" "$BASE/SHA256SUMS"; then
    (cd "$TMP" && grep " eap-bridge-linux-$ARCH\$" SHA256SUMS | sha256sum -c --quiet -) \
      || die "The downloaded file is damaged (checksum mismatch). Please try again."
    info "Checksum OK"
  else
    warn "Could not download SHA256SUMS – skipping the checksum test."
  fi
  SRC="$TMP/eap-bridge-linux-$ARCH"
fi
chmod +x "$SRC"
"$SRC" version >/dev/null 2>&1 || die "The program does not run on this Raspberry Pi ($ARCH)."

# ----------------------------------------------------------------- dependencies
say "Checking required system packages"
MISSING=()
[ -x /usr/sbin/dnsmasq ] || MISSING+=(dnsmasq-base)
if ! command -v nft >/dev/null 2>&1 && ! command -v iptables >/dev/null 2>&1; then
  MISSING+=(nftables)
fi
if [ ${#MISSING[@]} -gt 0 ]; then
  info "Installing: ${MISSING[*]}"
  if ! { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${MISSING[@]}"; }; then
    die "Could not install ${MISSING[*]}. The Raspberry Pi needs internet access for this step (for example via a phone hotspot)."
  fi
else
  info "All present (dnsmasq-base, nftables/iptables)"
fi
if systemctl is-enabled --quiet dnsmasq.service 2>/dev/null; then
  warn "The 'dnsmasq' service is enabled. It can conflict with the Ethernet sharing. Consider: sudo systemctl disable --now dnsmasq"
fi

# ----------------------------------------------------------------- the service
say "Installing the eap-bridge service"
systemctl stop eap-bridge.service 2>/dev/null || true
install -m 0755 "$SRC" "$BIN_DST"
install -d -m 0755 "$CONF_DIR"
info "Program: $BIN_DST ($("$BIN_DST" version))"

if [ ! -f "$CONF_FILE" ] && command -v ss >/dev/null 2>&1 && [ -n "$(ss -Hltn 'sport = :80' 2>/dev/null)" ]; then
  warn "Another program already uses port 80. The web interface will use port 8080 instead."
  printf '{\n  "listen": ":8080"\n}\n' > "$CONF_FILE"
  chmod 600 "$CONF_FILE"
fi

cat > "$UNIT_FILE" <<'UNIT'
[Unit]
Description=EAP Bridge - enterprise WiFi to Ethernet sharing with web interface
Wants=NetworkManager.service
After=NetworkManager.service

[Service]
Type=simple
ExecStart=/usr/local/bin/eap-bridge serve --config /etc/eap-bridge/config.json
Restart=always
RestartSec=5
ProtectHome=read-only
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --quiet eap-bridge.service
systemctl restart eap-bridge.service

# ---------------------------------------------------------------- WiFi country
say "Checking the WiFi country"
CURRENT=""
if command -v raspi-config >/dev/null 2>&1; then
  CURRENT="$(raspi-config nonint get_wifi_country 2>/dev/null || true)"
fi
if [ -z "$CURRENT" ] || [ "$CURRENT" = "00" ]; then
  CURRENT="$(iw reg get 2>/dev/null | sed -n 's/^country \([A-Z0-9][A-Z0-9]\):.*/\1/p' | head -n1 || true)"
fi
if [ -z "$CURRENT" ] || [ "$CURRENT" = "00" ]; then
  if [ -z "$COUNTRY" ]; then
    info "The WiFi country is not set yet. WiFi only works once it is known."
    COUNTRY="$(ask '    Two-letter country code (e.g. DE, GB, US, NL), or Enter to set it later in the web interface: ')"
  fi
  COUNTRY="$(printf '%s' "$COUNTRY" | tr '[:lower:]' '[:upper:]' | tr -d '[:space:]')"
  if [[ "$COUNTRY" =~ ^[A-Z]{2}$ ]]; then
    if command -v raspi-config >/dev/null 2>&1; then
      raspi-config nonint do_wifi_country "$COUNTRY" || warn "Setting the WiFi country failed."
    else
      iw reg set "$COUNTRY" || true
    fi
    info "WiFi country set to $COUNTRY"
  else
    info "Skipped – choose your country later under Settings in the web interface."
  fi
else
  info "WiFi country: $CURRENT"
fi
rfkill unblock wifi 2>/dev/null || true
nmcli radio wifi on 2>/dev/null || true

# ---------------------------------------------------------------- final checks
sleep 3
systemctl is-active --quiet eap-bridge.service \
  || die "The service did not start. Show the reason with: sudo journalctl -u eap-bridge -n 50"

LAN_IP="192.168.77.1"
if [ -f "$CONF_FILE" ]; then
  CFG_IP="$(sed -n 's/.*"lan_address": *"\([0-9.]*\)\/.*/\1/p' "$CONF_FILE" | head -n1)"
  [ -n "$CFG_IP" ] && LAN_IP="$CFG_IP"
fi
PORT_SUFFIX=""
grep -q '"listen": *":8080"' "$CONF_FILE" 2>/dev/null && PORT_SUFFIX=":8080"

ETH_LINE="$(nmcli -t -f DEVICE,STATE,CONNECTION device status 2>/dev/null | grep '^eth0:' || true)"
ETH_CONN="${ETH_LINE##*:}"
if [ -n "$ETH_CONN" ] && [ "$ETH_CONN" != "eap-bridge-lan" ] && [[ "$ETH_LINE" == *":connected:"* ]]; then
  warn "The Ethernet port is connected to another network right now (\"$ETH_CONN\", e.g. your router).
         The Pi shares its internet on the Ethernet port and must NOT stay connected to a router or a wall socket.
         Unplug that cable now. Then connect your switch or computer to the Pi."
fi

HOST="$(hostname)"
WIFI_CONN="$(nmcli -t -f DEVICE,STATE,CONNECTION device status 2>/dev/null | grep '^wlan0:connected:' | cut -d: -f3- || true)"

say "Done! EAP Bridge is installed and running."
cat <<EOF

    Next step: set up the WiFi in the web interface.

    1. Connect a computer to the Ethernet port of the Raspberry Pi
       (directly or through your switch) with a network cable.
    2. Open this address in its web browser:

           http://$LAN_IP$PORT_SUFFIX

       or  http://$HOST.local$PORT_SUFFIX
EOF
if [ -n "$WIFI_CONN" ] && [ "$WIFI_CONN" != "eap-bridge-wifi" ]; then
  cat <<EOF

    The Pi is currently connected to the WiFi "$WIFI_CONN". While it is, you can
    also open http://$HOST.local$PORT_SUFFIX from a device on that same WiFi.
EOF
fi
cat <<EOF

    Useful commands:  sudo eap-bridge status
                      sudo journalctl -u eap-bridge -f

EOF
