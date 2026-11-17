package bridge

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pkramme/rpi-eap-bridge/internal/diag"
	"github.com/pkramme/rpi-eap-bridge/internal/nm"
)

// CheckResult is the result of a connection test.
type CheckResult struct {
	Time    time.Time
	Items   []diag.Item
	Online  bool
	Sharing bool
	Summary string
}

func (c CheckResult) clone() CheckResult {
	c.Items = slices.Clone(c.Items)
	return c
}

// LastCheck returns the most recent connection test (if not too old).
func (m *Manager) LastCheck() *CheckResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastCheck == nil || time.Since(m.lastCheck.Time) > 30*time.Minute {
		return nil
	}
	c := m.lastCheck.clone()
	return &c
}

// RunCheck tests the whole chain step by step: WiFi login, IP address,
// internet, DNS, web, Ethernet cable and sharing.
func (m *Manager) RunCheck(ctx context.Context) CheckResult {
	m.checkMu.Lock()
	defer m.checkMu.Unlock()
	st := m.refreshStatus(ctx)
	var items []diag.Item
	add := func(name, status, detail, hint string) {
		items = append(items, diag.Item{Name: name, Status: status, Detail: detail, Hint: hint})
	}

	// 1. WiFi login
	w := st.WiFi
	const wifiName = "WiFi connection"
	switch {
	case st.NMError != "":
		add(wifiName, diag.Fail, st.NMError, "Restart the Raspberry Pi. If this stays, see the troubleshooting guide.")
	case w.Interface == "" || !w.Present:
		add(wifiName, diag.Fail, "No WiFi adapter was found.", "")
	case !w.RadioOn:
		add(wifiName, diag.Fail, "WiFi is switched off.", "Open Settings, set your WiFi country and switch WiFi on.")
	case !st.Configured:
		add(wifiName, diag.Fail, "No WiFi network has been set up yet.", "Open “WiFi setup” and enter your network and login.")
	case w.State == nm.StateActivated && w.Managed:
		detail := fmt.Sprintf("Connected to “%s” and logged in as %s.", st.Network.SSID, st.Network.Identity)
		status, hint := diag.OK, ""
		if w.Signal >= 0 {
			detail += fmt.Sprintf(" Signal strength %d%%.", w.Signal)
			if w.Signal < 35 {
				status = diag.Warn
				hint = "The signal is weak. Place the Raspberry Pi closer to an access point or a window for a faster, more reliable connection."
			}
		}
		add(wifiName, status, detail, hint)
	case w.State == nm.StateActivated:
		add(wifiName, diag.Warn, fmt.Sprintf("Connected to “%s” – not to the network you set up (“%s”).", firstNonEmpty(w.SSID, w.Connection), st.Network.SSID), "")
	case nm.IsConnecting(w.State):
		add(wifiName, diag.Warn, "Still connecting ("+strings.ToLower(w.Label())+")", "Wait a moment and run the test again.")
	case st.Paused:
		add(wifiName, diag.Fail, "You switched the WiFi connection off.", "Click “Connect” on the status page.")
	default:
		add(wifiName, diag.Fail, "Not connected to “"+st.Network.SSID+"”.", "Look at the error message on the status page or click “Connect now”.")
	}

	// 2. IP address
	if w.State == nm.StateActivated {
		if len(w.IPv4) > 0 {
			detail := "Got " + strings.Join(w.IPv4, ", ")
			if w.Gateway != "" {
				detail += ", gateway " + w.Gateway
			}
			add("IP address", diag.OK, detail+".", "")
		} else {
			add("IP address", diag.Fail, "The network did not hand out an IPv4 address.", "Some networks only accept registered devices. Ask your IT department.")
		}
	} else {
		add("IP address", diag.Skip, "Not tested – no WiFi connection.", "")
	}

	// 3-5. Internet, DNS, web
	online := false
	if w.State == nm.StateActivated && len(w.IPv4) > 0 {
		probes := m.opt.Probe(ctx)
		online = len(probes) > 0
		for _, p := range probes {
			items = append(items, p)
			if p.Status != diag.OK {
				online = false
			}
		}
	} else {
		for _, n := range []string{"Internet reachable", "Name lookup (DNS)", "Test website"} {
			add(n, diag.Skip, "Not tested – no WiFi connection.", "")
		}
	}

	// 6-8. Ethernet side
	l := st.LAN
	switch {
	case l.Interface == "" || !l.Present:
		add("Ethernet cable", diag.Fail, "No Ethernet port was found.", "A Raspberry Pi Zero needs a USB Ethernet adapter.")
	case l.Carrier:
		add("Ethernet cable", diag.OK, "A cable is plugged into "+l.Interface+".", "")
	default:
		add("Ethernet cable", diag.Warn, "No cable is plugged into the Ethernet port.", "Connect a switch or a device with a network cable.")
	}
	switch {
	case !l.Managing:
		add("Internet sharing", diag.Skip, "Ethernet sharing is switched off in the configuration (manage_lan).", "")
	case l.Active():
		add("Internet sharing", diag.OK, fmt.Sprintf("Active on %s. Devices get an address from %s automatically.", l.Interface, l.Prefix), "")
	case !l.Carrier:
		add("Internet sharing", diag.Skip, "Starts automatically as soon as a cable is plugged in.", "")
	case l.Connection != "" && !l.Managed:
		add("Internet sharing", diag.Fail, "The port is used by the connection “"+l.Connection+"”.", "Unplug that cable and connect your switch or device instead.")
	default:
		add("Internet sharing", diag.Fail, "Sharing is not active.", "Restart the Raspberry Pi. If it stays inactive, see the troubleshooting guide.")
	}
	if l.Active() {
		n := len(l.Clients)
		switch n {
		case 0:
			add("Connected devices", diag.Info, "No device has requested an address yet.", "Devices must be set to “automatic (DHCP)” network settings. Unplug and replug their cable if needed.")
		case 1:
			add("Connected devices", diag.Info, "1 device got an address.", "")
		default:
			add("Connected devices", diag.Info, fmt.Sprintf("%d devices got an address.", n), "")
		}
	}

	res := CheckResult{Time: time.Now(), Items: items, Online: online, Sharing: l.Active()}
	switch {
	case online && l.Active():
		res.Summary = "Everything works: the Raspberry Pi is online and shares the connection on its Ethernet port."
	case online:
		res.Summary = "The Raspberry Pi is online. Ethernet sharing is not active yet – see below."
	default:
		res.Summary = "The internet connection does not work yet. The red items below show where the problem is."
	}
	m.mu.Lock()
	c := res.clone()
	m.lastCheck = &c
	m.mu.Unlock()
	return res
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
