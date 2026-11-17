package bridge

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pkramme/rpi-eap-bridge/internal/nm"
)

// Network is a WiFi network (all access points with the same name).
type Network struct {
	SSID       string
	Signal     int
	Security   string
	Enterprise bool
	Bands      []string
	InUse      bool
}

// SecurityLabel describes the network's security in plain words.
func (n Network) SecurityLabel() string {
	switch {
	case n.Enterprise:
		return "Username + password (WPA-Enterprise)"
	case n.Security == "":
		return "Open network"
	case strings.Contains(n.Security, "WPA"):
		return "Password only (WPA-Personal)"
	default:
		return n.Security
	}
}

func bandOf(freq int) string {
	switch {
	case freq <= 0:
		return ""
	case freq < 3000:
		return "2.4 GHz"
	case freq < 5925:
		return "5 GHz"
	default:
		return "6 GHz"
	}
}

// GroupNetworks merges access points by network name and sorts the result:
// enterprise networks first, then by signal strength.
func GroupNetworks(aps []nm.AccessPoint) []Network {
	idx := map[string]int{}
	var out []Network
	for _, ap := range aps {
		if ap.SSID == "" || !utf8.ValidString(ap.SSID) {
			continue // hidden network or unsupported name
		}
		i, ok := idx[ap.SSID]
		if !ok {
			i = len(out)
			idx[ap.SSID] = i
			out = append(out, Network{SSID: ap.SSID, Signal: ap.Signal, Security: ap.Security})
		}
		n := &out[i]
		if ap.Signal > n.Signal {
			n.Signal = ap.Signal
		}
		if strings.Contains(ap.Security, "802.1X") {
			n.Enterprise = true
			n.Security = ap.Security
		}
		if ap.InUse {
			n.InUse = true
		}
		if b := bandOf(ap.FreqMHz); b != "" && !slices.Contains(n.Bands, b) {
			n.Bands = append(n.Bands, b)
		}
	}
	for i := range out {
		slices.Sort(out[i].Bands)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Enterprise != out[b].Enterprise {
			return out[a].Enterprise
		}
		return out[a].Signal > out[b].Signal
	})
	return out
}

// Scan returns the visible WiFi networks. Results are cached for a short
// time; rescan asks the WiFi adapter to search again.
func (m *Manager) Scan(ctx context.Context, rescan bool) ([]Network, error) {
	wifiIf, _ := m.Interfaces()
	if wifiIf == "" {
		return nil, errors.New("no WiFi adapter was found")
	}
	if !rescan {
		m.mu.Lock()
		if m.scan != nil && time.Since(m.scanAt) < 20*time.Second {
			nets := slices.Clone(m.scan)
			m.mu.Unlock()
			return nets, nil
		}
		m.mu.Unlock()
	}
	aps, err := m.nm.WiFiList(ctx, wifiIf, rescan)
	if err != nil && rescan {
		// NetworkManager refuses scans in quick succession; use what it has.
		aps, err = m.nm.WiFiList(ctx, wifiIf, false)
	}
	if err == nil && len(aps) == 0 && !rescan {
		aps, err = m.nm.WiFiList(ctx, wifiIf, true)
	}
	if err != nil {
		return nil, errors.New(nm.ErrorMessage(err))
	}
	nets := GroupNetworks(aps)
	m.mu.Lock()
	m.scan, m.scanAt = nets, time.Now()
	m.mu.Unlock()
	return slices.Clone(nets), nil
}

// KnownNetwork returns a network from the last scan.
func (m *Manager) KnownNetwork(ssid string) (Network, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.scan {
		if n.SSID == ssid {
			return n, true
		}
	}
	return Network{}, false
}
