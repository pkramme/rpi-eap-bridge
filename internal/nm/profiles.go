package nm

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"unicode/utf8"
)

const managedComment = "Managed by eap-bridge. Use the web interface to change it; manual edits will be overwritten."

// EAPProfile describes a WPA2/WPA3-Enterprise (802.1X) WiFi client profile.
type EAPProfile struct {
	ID        string
	UUID      string
	Interface string
	SSID      []byte
	Hidden    bool

	EAP           string // "peap" or "ttls"
	Phase2Auth    string // inner method, e.g. "mschapv2" or "pap"
	Phase2AuthEAP string // EAP inner method for TTLS, e.g. "mschapv2"

	Identity          string
	AnonymousIdentity string
	Password          string

	CACertPath        string
	SystemCACerts     bool
	DomainSuffixMatch string

	Autoconnect bool
	Priority    int
}

var (
	validEAP       = map[string]bool{"peap": true, "ttls": true}
	validPhase2    = map[string]bool{"pap": true, "chap": true, "mschap": true, "mschapv2": true, "gtc": true, "md5": true}
	validPhase2EAP = map[string]bool{"mschapv2": true, "gtc": true, "md5": true}
)

func checkValue(name, v string) error {
	if !utf8.ValidString(v) {
		return fmt.Errorf("%s contains invalid characters", name)
	}
	if strings.ContainsRune(v, 0) {
		return fmt.Errorf("%s contains a NUL character", name)
	}
	return nil
}

// Keyfile renders the profile in NetworkManager keyfile format.
func (p EAPProfile) Keyfile() ([]byte, error) {
	for name, v := range map[string]string{
		"id": p.ID, "uuid": p.UUID, "interface": p.Interface,
		"identity": p.Identity, "anonymous identity": p.AnonymousIdentity,
		"password": p.Password, "CA certificate path": p.CACertPath,
		"domain": p.DomainSuffixMatch,
	} {
		if err := checkValue(name, v); err != nil {
			return nil, err
		}
	}
	switch {
	case p.ID == "" || p.UUID == "":
		return nil, errors.New("profile id and uuid are required")
	case len(p.SSID) == 0 || len(p.SSID) > 32:
		return nil, errors.New("the network name must be 1 to 32 bytes long")
	case !validEAP[p.EAP]:
		return nil, fmt.Errorf("unsupported EAP method %q", p.EAP)
	case p.Phase2Auth != "" && p.Phase2AuthEAP != "":
		return nil, errors.New("only one inner authentication method may be set")
	case p.Phase2Auth == "" && p.Phase2AuthEAP == "":
		return nil, errors.New("an inner authentication method is required")
	case p.Phase2Auth != "" && !validPhase2[p.Phase2Auth]:
		return nil, fmt.Errorf("unsupported inner method %q", p.Phase2Auth)
	case p.Phase2AuthEAP != "" && (p.EAP != "ttls" || !validPhase2EAP[p.Phase2AuthEAP]):
		return nil, fmt.Errorf("unsupported inner EAP method %q", p.Phase2AuthEAP)
	case p.Identity == "":
		return nil, errors.New("a username is required")
	}

	k := NewKeyFile(managedComment)
	c := k.Section("connection")
	c.SetString("id", p.ID)
	c.SetString("uuid", p.UUID)
	c.SetRaw("type", "wifi")
	if p.Interface != "" {
		c.SetString("interface-name", p.Interface)
	}
	c.SetBool("autoconnect", p.Autoconnect)
	c.SetInt("autoconnect-priority", p.Priority)
	// 0 = retry forever; an unattended bridge should never give up.
	c.SetInt("autoconnect-retries", 0)

	w := k.Section("wifi")
	w.SetRaw("mode", "infrastructure")
	w.SetRaw("ssid", ByteList(p.SSID))
	if p.Hidden {
		w.SetBool("hidden", true)
	}
	// 2 = disable power saving: lower latency and fewer drop-outs.
	w.SetInt("powersave", 2)

	k.Section("wifi-security").SetRaw("key-mgmt", "wpa-eap")

	x := k.Section("802-1x")
	x.SetRaw("eap", p.EAP+";")
	x.SetString("identity", p.Identity)
	if p.AnonymousIdentity != "" {
		x.SetString("anonymous-identity", p.AnonymousIdentity)
	}
	x.SetString("password", p.Password)
	x.SetInt("password-flags", 0) // stored in this (root-only) file
	if p.Phase2Auth != "" {
		x.SetRaw("phase2-auth", p.Phase2Auth)
	}
	if p.Phase2AuthEAP != "" {
		x.SetRaw("phase2-autheap", p.Phase2AuthEAP)
	}
	if p.CACertPath != "" {
		x.SetString("ca-cert", p.CACertPath)
	}
	if p.SystemCACerts {
		x.SetBool("system-ca-certs", true)
	}
	if p.DomainSuffixMatch != "" {
		x.SetString("domain-suffix-match", p.DomainSuffixMatch)
	}

	ip4 := k.Section("ipv4")
	ip4.SetRaw("method", "auto")
	// Require IPv4: the Ethernet side is shared via IPv4 NAT, so an
	// IPv6-only uplink would be useless.
	ip4.SetBool("may-fail", false)
	k.Section("ipv6").SetRaw("method", "auto")
	k.Section("proxy")
	return k.Bytes(), nil
}

// LANProfile describes the Ethernet profile that shares the uplink. With
// ipv4.method=shared NetworkManager assigns Address to the port, runs a DHCP
// and DNS server (dnsmasq) and sets up NAT plus IP forwarding.
type LANProfile struct {
	ID        string
	UUID      string
	Interface string
	Address   netip.Prefix
	Priority  int
}

// Keyfile renders the profile in NetworkManager keyfile format.
func (p LANProfile) Keyfile() ([]byte, error) {
	for name, v := range map[string]string{"id": p.ID, "uuid": p.UUID, "interface": p.Interface} {
		if err := checkValue(name, v); err != nil {
			return nil, err
		}
	}
	if p.ID == "" || p.UUID == "" || p.Interface == "" {
		return nil, errors.New("profile id, uuid and interface are required")
	}
	if !p.Address.IsValid() || !p.Address.Addr().Is4() {
		return nil, errors.New("an IPv4 address is required")
	}
	k := NewKeyFile(managedComment)
	c := k.Section("connection")
	c.SetString("id", p.ID)
	c.SetString("uuid", p.UUID)
	c.SetRaw("type", "ethernet")
	c.SetString("interface-name", p.Interface)
	c.SetBool("autoconnect", true)
	c.SetInt("autoconnect-priority", p.Priority)
	c.SetInt("autoconnect-retries", 0)
	k.Section("ethernet")
	ip4 := k.Section("ipv4")
	ip4.SetRaw("address1", p.Address.String())
	ip4.SetRaw("method", "shared")
	k.Section("ipv6").SetRaw("method", "link-local")
	k.Section("proxy")
	return k.Bytes(), nil
}
