#!/usr/bin/env bash
# Builds the release files in dist/:
#   eap-bridge-linux-{arm64,armv6,amd64}   program for each processor type
#   install.sh, uninstall.sh                installers (repository name filled in)
#   eap-bridge-<version>.zip                offline bundle to copy onto the SD card
#   SHA256SUMS                              checksums
#
# Usage: VERSION=v1.0.0 REPO=owner/rpi-eap-bridge scripts/package-release.sh
set -euo pipefail

cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
REPO="${REPO:-}"
DIST=dist
STAGE=build/eap-bridge

rm -rf "$DIST" build
mkdir -p "$DIST" "$STAGE/bin"

build() { # goarch goarm name
  echo "building $3"
  CGO_ENABLED=0 GOOS=linux GOARCH="$1" GOARM="$2" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$DIST/$3" ./cmd/eap-bridge
}
build arm64 "" eap-bridge-linux-arm64
build arm 6 eap-bridge-linux-armv6
build amd64 "" eap-bridge-linux-amd64

for s in install.sh uninstall.sh; do
  if [ -n "$REPO" ]; then
    sed "s#YOUR-GITHUB-USER/rpi-eap-bridge#$REPO#g" "scripts/$s" > "$DIST/$s"
  else
    cp "scripts/$s" "$DIST/$s"
  fi
  chmod +x "$DIST/$s"
done

# Offline bundle: everything needed on the SD card, no internet required.
cp "$DIST"/eap-bridge-linux-* "$STAGE/bin/"
(cd "$STAGE/bin" && sha256sum eap-bridge-linux-* > SHA256SUMS 2>/dev/null || shasum -a 256 eap-bridge-linux-* > SHA256SUMS)
cp "$DIST/install.sh" "$DIST/uninstall.sh" "$STAGE/"
cat > "$STAGE/README.txt" <<EOF
EAP Bridge $VERSION – offline installation

1. Copy this whole "eap-bridge" folder onto the SD card's "bootfs" drive.
2. Start the Raspberry Pi and log in (keyboard + screen, or SSH).
3. Type:   sudo bash /boot/firmware/eap-bridge/install.sh
4. Connect a computer to the Pi's Ethernet port and open http://192.168.77.1

Full guide: docs/INSTALL.md in the project.
EOF
(cd build && rm -f "../$DIST/eap-bridge-$VERSION.zip" && zip -qr "../$DIST/eap-bridge-$VERSION.zip" eap-bridge)

(cd "$DIST" && { sha256sum eap-bridge-linux-* install.sh uninstall.sh "eap-bridge-$VERSION.zip" 2>/dev/null \
  || shasum -a 256 eap-bridge-linux-* install.sh uninstall.sh "eap-bridge-$VERSION.zip"; } > SHA256SUMS)
rm -rf build
echo
ls -l "$DIST"
