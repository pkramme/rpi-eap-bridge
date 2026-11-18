// Package demo simulates NetworkManager and the operating system so the web
// interface can be tried on any computer: `eap-bridge serve --demo`.
//
// Simulated behaviour: the password "wrong" is rejected, the network
// "CorpNet" only accepts PEAP, and any network name that is not in the scan
// list is "not found".
package demo

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkramme/rpi-eap-bridge/internal/nm"
	"github.com/pkramme/rpi-eap-bridge/internal/system"
)

type conn struct {
	id, uuid, typ, iface string
	ssid, password, eap  string
	address, pmf         string
}

type logLine struct {
	t    time.Time
	text string
}

// Demo implements nm.NM and system.System.
type Demo struct {
	// Speed scales all simulated delays (1 = realistic, 0.01 = for tests).
	Speed float64

	mu        sync.Mutex
	conns     map[string]*conn
	wifiState int
	wifiConn  string
	lanState  int
	lanConn   string
	radio     bool
	country   string
	journal   []logLine
}

var (
	_ nm.NM         = (*Demo)(nil)
	_ system.System = (*Demo)(nil)
)

// New returns a simulator with WiFi disconnected and no profiles.
func New() *Demo {
	return &Demo{
		Speed:     1,
		conns:     map[string]*conn{},
		wifiState: nm.StateDisconnected,
		lanState:  nm.StateDisconnected,
		radio:     true,
		country:   "DE",
	}
}

var accessPoints = []nm.AccessPoint{
	{SSID: "eduroam", BSSID: "AA:BB:CC:00:00:01", FreqMHz: 5180, Signal: 78, Security: "WPA2 802.1X"},
	{SSID: "eduroam", BSSID: "AA:BB:CC:00:00:02", FreqMHz: 2437, Signal: 64, Security: "WPA2 802.1X"},
	{SSID: "CorpNet", BSSID: "AA:BB:CC:00:01:01", FreqMHz: 5500, Signal: 52, Security: "WPA2 WPA3 802.1X"},
	{SSID: "Uni-Guest", BSSID: "AA:BB:CC:00:02:01", FreqMHz: 2412, Signal: 70, Security: ""},
	{SSID: "FRITZ!Box 7590 XY", BSSID: "AA:BB:CC:00:03:01", FreqMHz: 2462, Signal: 31, Security: "WPA2"},
	{SSID: "", BSSID: "AA:BB:CC:00:04:01", FreqMHz: 2437, Signal: 40, Security: "WPA2 802.1X"},
	// LegacyNet can only be joined with PMF switched off (like some Pi WiFi
	// chips behave); WPA3-Only can never be joined.
	{SSID: "LegacyNet", BSSID: "AA:BB:CC:00:05:01", FreqMHz: 2437, Signal: 60, Security: "WPA2 802.1X"},
	{SSID: "WPA3-Only", BSSID: "AA:BB:CC:00:06:01", FreqMHz: 5240, Signal: 58, Security: "WPA3 802.1X"},
}

var stateText = map[int]string{
	nm.StateUnavailable:  "unavailable",
	nm.StateDisconnected: "disconnected",
	nm.StatePrepare:      "connecting (prepare)",
	nm.StateConfig:       "connecting (configuring)",
	nm.StateIPConfig:     "connecting (getting IP configuration)",
	nm.StateActivated:    "connected",
}

func (d *Demo) sleep(ctx context.Context, dur time.Duration) error {
	select {
	case <-time.After(time.Duration(float64(dur) * d.Speed)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Demo) logf(unit, format string, args ...any) {
	d.journal = append(d.journal, logLine{time.Now(), unit + ": " + fmt.Sprintf(format, args...)})
}

func cmdErr(msg string) error { return &nm.CmdError{Stderr: "Error: " + msg} }

func (d *Demo) setWiFi(state int) {
	d.mu.Lock()
	d.wifiState = state
	d.mu.Unlock()
}

// Devices implements nm.NM.
func (d *Demo) Devices(ctx context.Context) ([]nm.Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return []nm.Device{
		{Name: "wlan0", Type: "wifi", State: stateText[d.wifiState]},
		{Name: "eth0", Type: "ethernet", State: stateText[d.lanState]},
		{Name: "lo", Type: "loopback", State: "unmanaged"},
	}, nil
}

// DeviceInfo implements nm.NM.
func (d *Demo) DeviceInfo(ctx context.Context, iface string) (nm.DeviceInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch iface {
	case "wlan0":
		st := d.wifiState
		if !d.radio {
			st = nm.StateUnavailable
		}
		info := nm.DeviceInfo{Name: iface, State: st, StateText: stateText[st], HWAddr: "DC:A6:32:12:34:56"}
		if c := d.conns[d.wifiConn]; c != nil && st != nm.StateDisconnected && st != nm.StateUnavailable {
			info.Connection, info.ConnUUID = c.id, c.uuid
		}
		if st == nm.StateActivated {
			info.IPv4, info.Gateway, info.DNS = []string{"10.23.45.67/16"}, "10.23.0.1", []string{"10.23.0.53"}
		}
		return info, nil
	case "eth0":
		info := nm.DeviceInfo{Name: iface, State: d.lanState, StateText: stateText[d.lanState], HWAddr: "DC:A6:32:65:43:21"}
		if c := d.conns[d.lanConn]; c != nil && d.lanState == nm.StateActivated {
			info.Connection, info.ConnUUID = c.id, c.uuid
			info.IPv4 = []string{c.address}
		}
		return info, nil
	}
	return nm.DeviceInfo{}, cmdErr(fmt.Sprintf("Device '%s' not found.", iface))
}

// WiFiList implements nm.NM.
func (d *Demo) WiFiList(ctx context.Context, iface string, rescan bool) ([]nm.AccessPoint, error) {
	if rescan {
		if err := d.sleep(ctx, 1500*time.Millisecond); err != nil {
			return nil, err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.radio {
		return nil, nil
	}
	active := ""
	if c := d.conns[d.wifiConn]; c != nil && d.wifiState == nm.StateActivated {
		active = c.ssid
	}
	aps := make([]nm.AccessPoint, len(accessPoints))
	copy(aps, accessPoints)
	for i := range aps {
		if active != "" && aps[i].SSID == active {
			aps[i].InUse = true
			break // the Pi is associated with one access point
		}
	}
	return aps, nil
}

// Connections implements nm.NM.
func (d *Demo) Connections(ctx context.Context) ([]nm.Connection, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []nm.Connection
	for _, c := range d.conns {
		out = append(out, nm.Connection{Name: c.id, UUID: c.uuid, Type: c.typ, Active: c.uuid == d.wifiConn || c.uuid == d.lanConn})
	}
	return out, nil
}

// Load implements nm.NM by parsing the keyfile.
func (d *Demo) Load(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return cmdErr(err.Error())
	}
	kf, err := nm.ParseKeyFile(data)
	if err != nil {
		return cmdErr(fmt.Sprintf("failed to load connection: %v", err))
	}
	c := &conn{
		id:      kf["connection"]["id"],
		uuid:    kf["connection"]["uuid"],
		typ:     kf["connection"]["type"],
		iface:   kf["connection"]["interface-name"],
		address: kf["ipv4"]["address1"],
		pmf:     kf["wifi-security"]["pmf"],
	}
	if c.uuid == "" {
		return cmdErr("failed to load connection: missing uuid")
	}
	if ssid, ok := nm.DecodeByteList(kf["wifi"]["ssid"]); ok {
		c.ssid = string(ssid)
	}
	if x := kf["802-1x"]; x != nil {
		c.password = x["password"]
		c.eap = strings.TrimSuffix(x["eap"], ";")
	}
	d.mu.Lock()
	d.conns[c.uuid] = c
	d.mu.Unlock()
	return nil
}

// Up implements nm.NM and simulates the activation steps.
func (d *Demo) Up(ctx context.Context, uuid, iface string, wait time.Duration) error {
	d.mu.Lock()
	c := d.conns[uuid]
	radio := d.radio
	d.mu.Unlock()
	if c == nil {
		return cmdErr(fmt.Sprintf("unknown connection '%s'.", uuid))
	}
	if c.typ == "ethernet" {
		d.mu.Lock()
		d.lanState, d.lanConn = nm.StatePrepare, uuid
		d.mu.Unlock()
		if err := d.sleep(ctx, 500*time.Millisecond); err != nil {
			return err
		}
		d.mu.Lock()
		d.lanState = nm.StateActivated
		d.mu.Unlock()
		return nil
	}
	if !radio {
		return cmdErr("Connection activation failed: No suitable device found for this connection (device wlan0 not available because device is not available).")
	}
	known := false
	for _, ap := range accessPoints {
		known = known || ap.SSID == c.ssid
	}

	d.mu.Lock()
	d.wifiState, d.wifiConn = nm.StatePrepare, uuid
	d.logf("NetworkManager[530]", "<info>  device (wlan0): Activation: starting connection '%s' (%s)", c.id, c.uuid)
	d.mu.Unlock()
	if err := d.sleep(ctx, 700*time.Millisecond); err != nil {
		return err
	}
	d.setWiFi(nm.StateConfig)

	fail := func(nmMsg string, logs ...string) error {
		d.mu.Lock()
		for _, l := range logs {
			d.logf("wpa_supplicant[612]", "wlan0: %s", l)
		}
		d.logf("NetworkManager[530]", "<info>  device (wlan0): state change: config -> failed")
		d.wifiState, d.wifiConn = nm.StateDisconnected, ""
		d.mu.Unlock()
		return cmdErr("Connection activation failed: " + nmMsg)
	}

	if !known {
		if err := d.sleep(ctx, 4*time.Second); err != nil {
			return err
		}
		return fail("The Wi-Fi network could not be found", "CTRL-EVENT-NETWORK-NOT-FOUND")
	}
	if c.ssid == "WPA3-Only" || (c.ssid == "LegacyNet" && c.pmf != "1") {
		if err := d.sleep(ctx, 3*time.Second); err != nil {
			return err
		}
		return fail("Secrets were required, but not provided",
			"Trying to associate with aa:bb:cc:00:05:01 (SSID='"+c.ssid+"' freq=2437 MHz)",
			"CTRL-EVENT-ASSOC-REJECT bssid=00:00:00:00:00:00 status_code=16",
			"CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid=\""+c.ssid+"\" auth_failures=1 duration=10 reason=CONN_FAILED")
	}
	d.mu.Lock()
	d.logf("wpa_supplicant[612]", "wlan0: CTRL-EVENT-EAP-STARTED EAP authentication started")
	d.logf("wpa_supplicant[612]", "wlan0: CTRL-EVENT-EAP-PROPOSED-METHOD vendor=0 method=25")
	d.mu.Unlock()
	if err := d.sleep(ctx, 2500*time.Millisecond); err != nil {
		return err
	}
	if c.ssid == "CorpNet" && c.eap != "peap" {
		return fail("Secrets were required, but not provided",
			"CTRL-EVENT-EAP-PROPOSED-METHOD vendor=0 method=25",
			"CTRL-EVENT-EAP-FAILURE EAP authentication failed")
	}
	d.mu.Lock()
	d.logf("wpa_supplicant[612]", "wlan0: CTRL-EVENT-EAP-METHOD EAP vendor 0 method 25 (PEAP) selected")
	d.logf("wpa_supplicant[612]", "wlan0: CTRL-EVENT-EAP-PEER-CERT depth=0 subject='/C=DE/O=Example University/CN=radius.example.edu' hash=4f2a…")
	d.mu.Unlock()
	if c.password == "wrong" {
		return fail("Secrets were required, but not provided",
			"CTRL-EVENT-EAP-FAILURE EAP authentication failed",
			"CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid=\""+c.ssid+"\" auth_failures=1 duration=10 reason=AUTH_FAILED")
	}
	d.mu.Lock()
	d.logf("wpa_supplicant[612]", "wlan0: CTRL-EVENT-EAP-SUCCESS EAP authentication completed successfully")
	d.wifiState = nm.StateIPConfig
	d.mu.Unlock()
	if err := d.sleep(ctx, 1500*time.Millisecond); err != nil {
		return err
	}
	d.mu.Lock()
	d.wifiState = nm.StateActivated
	d.logf("NetworkManager[530]", "<info>  device (wlan0): Activation: successful, device activated.")
	d.mu.Unlock()
	return nil
}

// Down implements nm.NM.
func (d *Demo) Down(ctx context.Context, uuid string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch uuid {
	case d.wifiConn:
		d.wifiState, d.wifiConn = nm.StateDisconnected, ""
	case d.lanConn:
		d.lanState, d.lanConn = nm.StateDisconnected, ""
	default:
		return cmdErr(fmt.Sprintf("'%s' is not an active connection.", uuid))
	}
	return nil
}

// Delete implements nm.NM.
func (d *Demo) Delete(ctx context.Context, uuid string) error {
	_ = d.Down(ctx, uuid)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conns[uuid] == nil {
		return cmdErr(fmt.Sprintf("unknown connection '%s'.", uuid))
	}
	delete(d.conns, uuid)
	return nil
}

// Secret implements nm.NM.
func (d *Demo) Secret(ctx context.Context, uuid, key string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c := d.conns[uuid]; c != nil && key == "802-1x.password" {
		return c.password, nil
	}
	return "", cmdErr("no such connection profile.")
}

// WiFiEnabled implements nm.NM.
func (d *Demo) WiFiEnabled(ctx context.Context) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.radio, nil
}

// SetWiFiEnabled implements nm.NM.
func (d *Demo) SetWiFiEnabled(ctx context.Context, on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.radio = on
	if !on {
		d.wifiState, d.wifiConn = nm.StateUnavailable, ""
	} else if d.wifiState == nm.StateUnavailable {
		d.wifiState = nm.StateDisconnected
	}
	return nil
}

// Hostname implements system.System.
func (d *Demo) Hostname() string { return "eap-bridge" }

// Carrier implements system.System.
func (d *Demo) Carrier(iface string) bool { return iface == "eth0" }

// Leases implements system.System.
func (d *Demo) Leases(iface string) ([]system.Lease, error) {
	exp := time.Now().Add(50 * time.Minute)
	return []system.Lease{
		{Expires: exp, MAC: "3c:22:fb:10:20:30", IP: "192.168.77.23", Hostname: "PlayStation"},
		{Expires: exp, MAC: "f0:18:98:aa:bb:cc", IP: "192.168.77.57", Hostname: "living-room-tv"},
		{Expires: exp, MAC: "00:1b:21:de:ad:01", IP: "192.168.77.140", Hostname: ""},
	}, nil
}

// Journal implements system.System.
func (d *Demo) Journal(ctx context.Context, since time.Time, max int) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, l := range d.journal {
		if !since.IsZero() && l.t.Before(since) {
			continue
		}
		out = append(out, l.t.Format("2006-01-02T15:04:05-0700")+" eap-bridge "+l.text)
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out, nil
}

// WiFiCountry implements system.System.
func (d *Demo) WiFiCountry(ctx context.Context) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.country
}

// SetWiFiCountry implements system.System.
func (d *Demo) SetWiFiCountry(ctx context.Context, code string) error {
	if !system.ValidCountry(code) {
		return fmt.Errorf("invalid country code %q", code)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.country = code
	return nil
}

// Reboot implements system.System (no-op in the demo).
func (d *Demo) Reboot(ctx context.Context) error { return nil }

// PowerOff implements system.System (no-op in the demo).
func (d *Demo) PowerOff(ctx context.Context) error { return nil }
