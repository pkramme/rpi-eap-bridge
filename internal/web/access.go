package web

import (
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// policy decides who may open the web interface. Devices on the Ethernet
// side may always. Devices on the WiFi side may only while the Pi is NOT on
// the enterprise network (for example during setup over a phone hotspot)
// and only if they are on the same local network: the admin page must never
// be reachable for strangers on a university or company network.
//
// The decision uses the remote address: a TCP connection from an address of
// the Ethernet subnet can only be completed by a device on the Ethernet
// side, because the answers are routed there.
type policy struct {
	LAN           netip.Prefix
	LANIf         string
	WiFiIf        string
	WiFiPrefixes  []netip.Prefix
	UplinkManaged bool
	Mode          string // "auto", "always" or "never"
}

func (p policy) allow(remoteAddr string) bool {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return false
	}
	addr := ap.Addr()
	zone := addr.Zone()
	addr = addr.WithZone("").Unmap()

	if addr.IsLoopback() {
		return true
	}
	if addr.Is4() && p.LAN.IsValid() && p.LAN.Contains(addr) {
		return true
	}
	if addr.Is6() && addr.IsLinkLocalUnicast() && zone != "" && zone == p.LANIf {
		return true
	}

	// WiFi side
	if p.Mode == "never" || (p.Mode != "always" && p.UplinkManaged) {
		return false
	}
	if addr.Is4() {
		for _, pr := range p.WiFiPrefixes {
			if pr.Contains(addr) {
				return true
			}
		}
	}
	return addr.Is6() && addr.IsLinkLocalUnicast() && zone != "" && zone == p.WiFiIf
}

// hostAllowed protects against DNS rebinding: a malicious web site must not
// be able to talk to the bridge under a host name of its own.
func hostAllowed(host, hostname string, extra []string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(strings.ToLower(strings.Trim(h, "[]")), ".")
	if i := strings.IndexByte(h, '%'); i >= 0 {
		h = h[:i] // IPv6 zone
	}
	if h == "" || h == "localhost" {
		return true
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	hn := strings.ToLower(hostname)
	if hn != "" && (h == hn || h == hn+".local") {
		return true
	}
	for _, e := range extra {
		if strings.EqualFold(strings.TrimSuffix(e, "."), h) {
			return true
		}
	}
	return false
}

// sameOrigin rejects cross-site form submissions using the headers that
// browsers attach to every request.
func sameOrigin(fetchSite, origin, host string) bool {
	if fetchSite != "" {
		return fetchSite == "same-origin" || fetchSite == "none"
	}
	if origin == "" {
		return true // not a browser, or a very old one; the CSRF token still applies
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, host)
}
