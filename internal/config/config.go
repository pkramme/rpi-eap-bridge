// Package config loads and stores the bridge configuration
// (/etc/eap-bridge/config.json).
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"slices"
	"sync"

	"github.com/pkramme/rpi-eap-bridge/internal/fsutil"
)

// DefaultPath is where the configuration lives on the Raspberry Pi.
const DefaultPath = "/etc/eap-bridge/config.json"

// WiFi holds the non-secret part of the enterprise WiFi settings. The
// password itself is only stored in the NetworkManager connection profile.
type WiFi struct {
	SSID              string `json:"ssid"`
	Hidden            bool   `json:"hidden,omitempty"`
	EAP               string `json:"eap"`    // "peap" or "ttls"
	Phase2            string `json:"phase2"` // e.g. "mschapv2", "pap", "eap-mschapv2"
	Identity          string `json:"identity"`
	AnonymousIdentity string `json:"anonymous_identity,omitempty"`
	CAMode            string `json:"ca_mode"` // "system", "file" or "none"
	Domain            string `json:"domain,omitempty"`
	CASubject         string `json:"ca_subject,omitempty"`
	UUID              string `json:"uuid"`
}

// Config is the complete bridge configuration.
type Config struct {
	// Listen is the address of the web interface, e.g. ":80".
	Listen string `json:"listen"`
	// WiFiInterface and LANInterface are detected automatically when empty.
	WiFiInterface string `json:"wifi_interface"`
	LANInterface  string `json:"lan_interface"`
	// ManageLAN controls whether the bridge creates the Ethernet sharing profile.
	ManageLAN bool `json:"manage_lan"`
	// LANAddress is the address of the Raspberry Pi on the Ethernet side.
	LANAddress string `json:"lan_address"`
	LANUUID    string `json:"lan_uuid,omitempty"`
	// UplinkAccess controls whether the web interface may be opened from the
	// WiFi side: "auto" (only while the Pi is NOT on the enterprise network,
	// e.g. during setup over a phone hotspot), "always" or "never".
	UplinkAccess string `json:"uplink_access"`
	// AllowedHosts lists extra host names under which the web interface may
	// be opened (IP addresses, <hostname> and <hostname>.local always work).
	AllowedHosts      []string `json:"allowed_hosts,omitempty"`
	AdminPasswordHash string   `json:"admin_password_hash,omitempty"`
	SessionSecret     string   `json:"session_secret,omitempty"`
	WiFi              *WiFi    `json:"wifi,omitempty"`
	// Paused is true after the user clicked "Disconnect".
	Paused bool `json:"paused,omitempty"`
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Listen:       ":80",
		ManageLAN:    true,
		LANAddress:   "192.168.77.1/24",
		UplinkAccess: "auto",
	}
}

// LANPrefix parses and validates LANAddress.
func (c Config) LANPrefix() (netip.Prefix, error) {
	p, err := netip.ParsePrefix(c.LANAddress)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("lan_address %q is not valid (expected something like 192.168.77.1/24)", c.LANAddress)
	}
	if !p.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("lan_address %q must be an IPv4 address", c.LANAddress)
	}
	if p.Bits() < 16 || p.Bits() > 29 {
		return netip.Prefix{}, fmt.Errorf("lan_address %q: the prefix length must be between /16 and /29", c.LANAddress)
	}
	if p.Addr() == p.Masked().Addr() {
		return netip.Prefix{}, fmt.Errorf("lan_address %q must be a host address such as 192.168.77.1/24, not the network address", c.LANAddress)
	}
	return p, nil
}

// LANIP returns the IP address of the Pi on the Ethernet side, or "".
func (c Config) LANIP() string {
	p, err := c.LANPrefix()
	if err != nil {
		return ""
	}
	return p.Addr().String()
}

// Validate checks the configuration for obvious mistakes.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen must not be empty")
	}
	if _, err := c.LANPrefix(); err != nil {
		return err
	}
	switch c.UplinkAccess {
	case "auto", "always", "never":
	default:
		return fmt.Errorf("uplink_access must be \"auto\", \"always\" or \"never\", not %q", c.UplinkAccess)
	}
	return nil
}

func (c Config) clone() Config {
	out := c
	out.AllowedHosts = slices.Clone(c.AllowedHosts)
	if c.WiFi != nil {
		w := *c.WiFi
		out.WiFi = &w
	}
	return out
}

// Store gives synchronized access to the configuration and persists changes.
type Store struct {
	path string
	mu   sync.Mutex
	cfg  Config
}

// Open loads the configuration from path. A missing file is created with
// default values.
func Open(path string) (*Store, error) {
	cfg := Default()
	data, readErr := os.ReadFile(path)
	switch {
	case readErr == nil:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
	case errors.Is(readErr, fs.ErrNotExist):
	default:
		return nil, readErr
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s := &Store{path: path, cfg: cfg}
	changed := readErr != nil
	if s.cfg.SessionSecret == "" {
		s.cfg.SessionSecret = RandomHex(32)
		changed = true
	}
	if s.cfg.LANUUID == "" {
		s.cfg.LANUUID = NewUUID()
		changed = true
	}
	if changed {
		if err := s.save(s.cfg); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Path returns the file name of the configuration.
func (s *Store) Path() string { return s.path }

// Get returns a copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.clone()
}

// Update applies fn to a copy of the configuration, validates and saves it.
// The in-memory configuration only changes if saving succeeded.
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg.clone()
	if err := fn(&c); err != nil {
		return err
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if err := s.save(c); err != nil {
		return err
	}
	s.cfg = c
	return nil
}

func (s *Store) save(c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// 0600: the file contains the admin password hash and session secret.
	return fsutil.WriteFileAtomic(s.path, data, 0o600)
}

// RandomHex returns n random bytes, hex encoded.
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// NewUUID returns a random (version 4) UUID.
func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
