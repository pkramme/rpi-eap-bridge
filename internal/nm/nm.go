// Package nm talks to NetworkManager (via nmcli) and writes NetworkManager
// keyfile connection profiles.
package nm

import (
	"context"
	"time"
)

// NetworkManager device states (NMDeviceState).
const (
	StateUnknown      = 0
	StateUnmanaged    = 10
	StateUnavailable  = 20
	StateDisconnected = 30
	StatePrepare      = 40
	StateConfig       = 50
	StateNeedAuth     = 60
	StateIPConfig     = 70
	StateIPCheck      = 80
	StateSecondaries  = 90
	StateActivated    = 100
	StateDeactivating = 110
	StateFailed       = 120
)

// IsConnecting reports whether a device state is one of the activation steps.
func IsConnecting(state int) bool { return state >= StatePrepare && state <= StateSecondaries }

// Device is one line of `nmcli device status`.
type Device struct {
	Name       string
	Type       string // "wifi", "ethernet", "loopback", ...
	State      string
	Connection string
}

// DeviceInfo is the subset of `nmcli device show` we use.
type DeviceInfo struct {
	Name       string
	State      int
	StateText  string
	Reason     string
	Connection string
	ConnUUID   string
	HWAddr     string
	IPv4       []string // CIDR notation, e.g. "10.1.2.3/16"
	Gateway    string
	DNS        []string
}

// AccessPoint is one line of `nmcli device wifi list`.
type AccessPoint struct {
	InUse    bool
	SSID     string
	BSSID    string
	FreqMHz  int
	Signal   int // 0-100
	Security string
}

// Connection is one line of `nmcli connection show`.
type Connection struct {
	Name   string
	UUID   string
	Type   string
	Device string
	Active bool
}

// NM is the set of NetworkManager operations the bridge needs. Nmcli is the
// real implementation; the demo package provides a simulated one.
type NM interface {
	Devices(ctx context.Context) ([]Device, error)
	DeviceInfo(ctx context.Context, iface string) (DeviceInfo, error)
	WiFiList(ctx context.Context, iface string, rescan bool) ([]AccessPoint, error)
	Connections(ctx context.Context) ([]Connection, error)
	// Load (re)reads a keyfile profile from disk.
	Load(ctx context.Context, path string) error
	// Up activates a profile and waits until it is active or failed.
	Up(ctx context.Context, uuid, iface string, wait time.Duration) error
	Down(ctx context.Context, uuid string) error
	Delete(ctx context.Context, uuid string) error
	// Secret returns a stored secret such as "802-1x.password".
	Secret(ctx context.Context, uuid, key string) (string, error)
	WiFiEnabled(ctx context.Context) (bool, error)
	SetWiFiEnabled(ctx context.Context, on bool) error
}
