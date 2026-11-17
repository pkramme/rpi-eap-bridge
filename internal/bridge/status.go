package bridge

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/pkramme/rpi-eap-bridge/internal/config"
	"github.com/pkramme/rpi-eap-bridge/internal/nm"
	"github.com/pkramme/rpi-eap-bridge/internal/system"
)

// WiFiStatus describes the WiFi (uplink) side.
type WiFiStatus struct {
	Interface  string
	Present    bool
	RadioOn    bool
	State      int
	StateText  string
	Connection string
	Managed    bool // the active connection is the one set up by this bridge
	SSID       string
	Signal     int // 0-100, -1 = unknown
	FreqMHz    int
	HWAddr     string
	IPv4       []string
	Gateway    string
	DNS        []string
}

// Label is a short human readable state.
func (w WiFiStatus) Label() string {
	switch {
	case w.Interface == "" || !w.Present:
		return "No WiFi adapter"
	case !w.RadioOn:
		return "WiFi switched off"
	}
	switch w.State {
	case nm.StateActivated:
		return "Connected"
	case nm.StatePrepare:
		return "Preparing…"
	case nm.StateConfig:
		return "Connecting and logging in…"
	case nm.StateNeedAuth:
		return "Waiting for login details…"
	case nm.StateIPConfig:
		return "Getting an IP address…"
	case nm.StateIPCheck, nm.StateSecondaries:
		return "Checking the connection…"
	case nm.StateDeactivating:
		return "Disconnecting…"
	case nm.StateFailed:
		return "Connection failed"
	case nm.StateUnavailable:
		return "WiFi not available"
	case nm.StateUnmanaged:
		return "Not managed"
	default:
		return "Not connected"
	}
}

// Tone is used as CSS class: ok, busy, warn, bad or off.
func (w WiFiStatus) Tone() string {
	switch {
	case !w.Present || !w.RadioOn:
		return "bad"
	case w.State == nm.StateActivated && w.Managed:
		return "ok"
	case w.State == nm.StateActivated:
		return "warn"
	case nm.IsConnecting(w.State) || w.State == nm.StateDeactivating:
		return "busy"
	default:
		return "bad"
	}
}

// LANStatus describes the Ethernet (sharing) side.
type LANStatus struct {
	Interface  string
	Present    bool
	Managing   bool // manage_lan is enabled
	Carrier    bool
	State      int
	Connection string
	Managed    bool // the sharing profile of this bridge is active
	Address    string
	Prefix     string
	Clients    []system.Lease
}

// Active reports whether internet sharing runs on the Ethernet port.
func (l LANStatus) Active() bool { return l.Managed && l.State == nm.StateActivated }

// Label is a short human readable state.
func (l LANStatus) Label() string {
	switch {
	case l.Interface == "" || !l.Present:
		return "No Ethernet port"
	case l.Active():
		return "Sharing active"
	case !l.Managing:
		return "Not managed by the bridge"
	case !l.Carrier:
		return "No cable plugged in"
	case l.Connection != "" && !l.Managed:
		return "Port used by another connection"
	case nm.IsConnecting(l.State):
		return "Starting…"
	default:
		return "Not active"
	}
}

// Tone is used as CSS class.
func (l LANStatus) Tone() string {
	switch {
	case l.Active():
		return "ok"
	case !l.Carrier && l.Present && l.Managing:
		return "off"
	case nm.IsConnecting(l.State):
		return "busy"
	case l.Connection != "" && !l.Managed:
		return "warn"
	default:
		return "bad"
	}
}

// Status is a snapshot of the whole bridge.
type Status struct {
	Time       time.Time
	NMError    string
	WiFi       WiFiStatus
	LAN        LANStatus
	Configured bool
	Paused     bool
	Network    *config.WiFi
	Country    string
	NextRetry  time.Time
	Warnings   []string
}

// Online reports whether the bridge is connected to its enterprise network.
func (s Status) Online() bool {
	return s.WiFi.State == nm.StateActivated && s.WiFi.Managed && len(s.WiFi.IPv4) > 0
}

// Status returns a recent status snapshot (at most two seconds old).
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	st := m.status
	m.mu.Unlock()
	if !st.Time.IsZero() && time.Since(st.Time) < 2*time.Second {
		return st
	}
	return m.refreshStatus(ctx)
}

func (m *Manager) invalidateStatus() {
	m.mu.Lock()
	m.status.Time = time.Time{}
	m.mu.Unlock()
}

func (m *Manager) refreshStatus(ctx context.Context) Status {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cfg := m.cfg.Get()
	wifiIf, lanIf := m.Interfaces()
	st := Status{Time: time.Now(), Configured: cfg.WiFi != nil, Paused: cfg.Paused, Network: cfg.WiFi}
	noteErr := func(err error) {
		if st.NMError == "" {
			msg := nm.ErrorMessage(err)
			if strings.Contains(msg, "executable file not found") {
				msg = "nmcli was not found. This bridge needs Raspberry Pi OS Bookworm or newer (with NetworkManager)."
			}
			st.NMError = msg
		}
	}

	// WiFi side
	st.WiFi.Interface = wifiIf
	st.WiFi.Signal = -1
	if wifiIf != "" {
		if info, err := m.nm.DeviceInfo(ctx, wifiIf); err != nil {
			noteErr(err)
		} else {
			st.WiFi.Present = true
			st.WiFi.State, st.WiFi.StateText = info.State, info.StateText
			st.WiFi.Connection = info.Connection
			st.WiFi.HWAddr = info.HWAddr
			st.WiFi.IPv4, st.WiFi.Gateway, st.WiFi.DNS = info.IPv4, info.Gateway, info.DNS
			st.WiFi.Managed = cfg.WiFi != nil && (info.ConnUUID == cfg.WiFi.UUID || (info.ConnUUID == "" && info.Connection == WiFiConnID))
			if info.State == nm.StateActivated {
				if aps, err := m.nm.WiFiList(ctx, wifiIf, false); err == nil {
					for _, ap := range aps {
						if ap.InUse {
							st.WiFi.SSID, st.WiFi.Signal, st.WiFi.FreqMHz = ap.SSID, ap.Signal, ap.FreqMHz
							break
						}
					}
				}
			}
		}
		if on, err := m.nm.WiFiEnabled(ctx); err != nil {
			noteErr(err)
		} else {
			st.WiFi.RadioOn = on
		}
	}

	// Ethernet side
	prefix, _ := cfg.LANPrefix()
	st.LAN.Interface, st.LAN.Managing = lanIf, cfg.ManageLAN
	st.LAN.Address, st.LAN.Prefix = prefix.Addr().String(), prefix.Masked().String()
	if lanIf != "" {
		if info, err := m.nm.DeviceInfo(ctx, lanIf); err != nil {
			noteErr(err)
		} else {
			st.LAN.Present = true
			st.LAN.State = info.State
			st.LAN.Connection = info.Connection
			st.LAN.Managed = info.ConnUUID == cfg.LANUUID || (info.ConnUUID == "" && info.Connection == LANConnID)
		}
		st.LAN.Carrier = m.sys.Carrier(lanIf)
		if st.LAN.Active() {
			if leases, err := m.sys.Leases(lanIf); err == nil {
				st.LAN.Clients = leases
			}
		}
	}
	st.Country = m.WiFiCountry(ctx, false)

	// Warnings
	if wifiIf == "" && st.NMError == "" {
		st.Warnings = append(st.Warnings, "No WiFi adapter was found on this Raspberry Pi.")
	}
	if lanIf == "" && st.NMError == "" {
		st.Warnings = append(st.Warnings, "No Ethernet port was found. (A Raspberry Pi Zero needs a USB Ethernet adapter.)")
	}
	if cfg.ManageLAN && st.LAN.Present && st.LAN.Connection != "" && !st.LAN.Managed {
		st.Warnings = append(st.Warnings, fmt.Sprintf(
			"The Ethernet port is currently used by the connection “%s” (for example a cable to a router). Internet sharing starts when you unplug that cable and connect your switch or device instead. Never connect the Pi's Ethernet port to a router or wall socket while sharing is active.",
			st.LAN.Connection))
	}
	for _, a := range st.WiFi.IPv4 {
		if p, err := netip.ParsePrefix(a); err == nil && prefix.IsValid() && p.Masked().Overlaps(prefix.Masked()) {
			st.Warnings = append(st.Warnings, fmt.Sprintf(
				"The WiFi network uses the same address range (%s) as the Ethernet side (%s). Devices behind the bridge will not work. Change \"lan_address\" in %s (for example to 192.168.88.1/24) and restart the Raspberry Pi.",
				p.Masked(), prefix.Masked(), m.cfg.Path()))
		}
	}
	if st.Configured && st.WiFi.State == nm.StateActivated && !st.WiFi.Managed {
		name := st.WiFi.SSID
		if name == "" {
			name = st.WiFi.Connection
		}
		st.Warnings = append(st.Warnings, fmt.Sprintf("The Raspberry Pi is connected to “%s” instead of “%s”. It will switch to your network automatically when it can reach it.", name, cfg.WiFi.SSID))
	}

	m.mu.Lock()
	if st.Configured && !st.Paused && !st.Online() {
		st.NextRetry = m.retry.next
	}
	m.status = st
	m.mu.Unlock()
	return st
}

// WiFiCountry returns the WiFi country, cached for ten minutes.
func (m *Manager) WiFiCountry(ctx context.Context, force bool) string {
	m.mu.Lock()
	cc, at := m.country, m.countryAt
	m.mu.Unlock()
	if !force && !at.IsZero() && time.Since(at) < 10*time.Minute {
		return cc
	}
	cc = m.sys.WiFiCountry(ctx)
	m.mu.Lock()
	m.country, m.countryAt = cc, time.Now()
	m.mu.Unlock()
	return cc
}

// AccessInfo is what the web server needs to decide who may open it.
type AccessInfo struct {
	LANIf         string
	WiFiIf        string
	WiFiPrefixes  []netip.Prefix
	UplinkManaged bool // the WiFi uses the enterprise connection (or unknown)
}

// AccessInfo returns the current access information.
func (m *Manager) AccessInfo() AccessInfo {
	m.mu.Lock()
	st := m.status
	ai := AccessInfo{LANIf: m.lanIf, WiFiIf: m.wifiIf}
	m.mu.Unlock()
	if st.WiFi.Interface == "" && st.LAN.Interface == "" {
		ai.UplinkManaged = true // no status yet: be safe
		return ai
	}
	ai.UplinkManaged = st.WiFi.Managed
	for _, a := range st.WiFi.IPv4 {
		if p, err := netip.ParsePrefix(a); err == nil {
			ai.WiFiPrefixes = append(ai.WiFiPrefixes, p.Masked())
		}
	}
	return ai
}
