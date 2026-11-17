#!/usr/bin/env bash
# Removes EAP Bridge.
#
#   sudo bash uninstall.sh           remove the program and web interface; the
#                                    WiFi login and Ethernet sharing keep working
#   sudo bash uninstall.sh --purge   also delete the WiFi login, the Ethernet
#                                    sharing and all settings
set -euo pipefail

PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1

[ "$(id -u)" -eq 0 ] || { echo "Please run with sudo: sudo bash $0 $*" >&2; exit 1; }

echo "==> Stopping the eap-bridge service"
systemctl disable --now eap-bridge.service 2>/dev/null || true
rm -f /etc/systemd/system/eap-bridge.service
systemctl daemon-reload
rm -f /usr/local/bin/eap-bridge

if [ "$PURGE" -eq 1 ]; then
  echo "==> Deleting the WiFi login and the Ethernet sharing"
  for c in eap-bridge-wifi eap-bridge-lan; do
    nmcli connection delete id "$c" >/dev/null 2>&1 || true
    rm -f "/etc/NetworkManager/system-connections/$c.nmconnection"
  done
  rm -rf /etc/eap-bridge
  echo "Done. The Ethernet port works as a normal network port again."
else
  echo "Done. The web interface is removed."
  echo "The WiFi login and the Ethernet sharing are still active (NetworkManager keeps them)."
  echo "To remove them as well, run: sudo bash $0 --purge"
fi
