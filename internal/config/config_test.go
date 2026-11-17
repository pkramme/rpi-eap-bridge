package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Get()
	if c.Listen != ":80" || !c.ManageLAN || c.LANIP() != "192.168.77.1" || c.SessionSecret == "" || c.LANUUID == "" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}

	if err := s.Update(func(c *Config) error { c.WiFi = &WiFi{SSID: "eduroam"}; c.Paused = true; return nil }); err != nil {
		t.Fatal(err)
	}
	// A partial file keeps the defaults for missing keys.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c2 := s2.Get()
	if c2.WiFi == nil || c2.WiFi.SSID != "eduroam" || !c2.Paused || c2.SessionSecret != c.SessionSecret {
		t.Fatalf("not persisted: %+v", c2)
	}
	// Get returns a copy.
	c2.WiFi.SSID = "changed"
	if s2.Get().WiFi.SSID != "eduroam" {
		t.Error("Get does not copy")
	}
}

func TestPartialFileAndValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen": ":8080"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := s.Get(); c.Listen != ":8080" || !c.ManageLAN || c.UplinkAccess != "auto" {
		t.Fatalf("defaults lost: %+v", c)
	}
	if err := s.Update(func(c *Config) error { c.LANAddress = "192.168.77.0/24"; return nil }); err == nil {
		t.Error("network address accepted as lan_address")
	}
	if s.Get().LANAddress != "192.168.77.1/24" {
		t.Error("invalid update was applied")
	}
	if err := os.WriteFile(path, []byte(`{"lan_address": "fe80::1/64"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("IPv6 lan_address accepted")
	}
}

func TestNewUUID(t *testing.T) {
	u := NewUUID()
	if len(u) != 36 || u[14] != '4' || u == NewUUID() {
		t.Fatalf("bad uuid %q", u)
	}
}
