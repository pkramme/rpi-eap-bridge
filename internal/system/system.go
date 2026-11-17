// Package system wraps the operating system functions the bridge needs
// besides NetworkManager: link state, DHCP leases, logs, WiFi country and
// power control.
package system

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Lease is a DHCP lease handed out to a device on the Ethernet side.
type Lease struct {
	Expires  time.Time // zero = never
	MAC      string
	IP       string
	Hostname string
}

// System is implemented by Linux (real) and the demo package (simulated).
type System interface {
	Hostname() string
	Carrier(iface string) bool
	Leases(iface string) ([]Lease, error)
	Journal(ctx context.Context, since time.Time, max int) ([]string, error)
	WiFiCountry(ctx context.Context) string
	SetWiFiCountry(ctx context.Context, code string) error
	Reboot(ctx context.Context) error
	PowerOff(ctx context.Context) error
}

// Linux is the real implementation for Raspberry Pi OS.
type Linux struct {
	// LeaseDir is where NetworkManager's dnsmasq stores leases.
	LeaseDir string
}

var _ System = (*Linux)(nil)

var (
	reIface   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)
	reCountry = regexp.MustCompile(`^[A-Z]{2}$`)
)

// ValidCountry reports whether code looks like an ISO 3166 alpha-2 code.
func ValidCountry(code string) bool { return reCountry.MatchString(code) && code != "00" }

// Hostname returns the host name of the Pi.
func (l *Linux) Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "raspberrypi"
	}
	return h
}

// Carrier reports whether a cable is plugged into iface.
func (l *Linux) Carrier(iface string) bool {
	if !reIface.MatchString(iface) {
		return false
	}
	b, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "carrier"))
	return err == nil && strings.TrimSpace(string(b)) == "1"
}

// Leases returns the current DHCP leases of NetworkManager's shared-mode
// dnsmasq on iface.
func (l *Linux) Leases(iface string) ([]Lease, error) {
	if !reIface.MatchString(iface) {
		return nil, fmt.Errorf("invalid interface name %q", iface)
	}
	dir := l.LeaseDir
	if dir == "" {
		dir = "/var/lib/NetworkManager"
	}
	data, err := os.ReadFile(filepath.Join(dir, "dnsmasq-"+iface+".leases"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseLeases(data, time.Now()), nil
}

// ParseLeases parses a dnsmasq lease file and drops expired leases.
// Line format: <expiry> <mac> <ip> <hostname|*> <client-id|*>
func ParseLeases(data []byte, now time.Time) []Lease {
	var out []Lease
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		exp, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		l := Lease{MAC: f[1], IP: f[2]}
		if exp != 0 {
			l.Expires = time.Unix(exp, 0)
			if l.Expires.Before(now) {
				continue
			}
		}
		if f[3] != "*" {
			l.Hostname = f[3]
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return ipLess(out[i].IP, out[j].IP) })
	return out
}

func ipLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	if len(pa) == 4 && len(pb) == 4 {
		for i := range pa {
			x, _ := strconv.Atoi(pa[i])
			y, _ := strconv.Atoi(pb[i])
			if x != y {
				return x < y
			}
		}
		return false
	}
	return a < b
}

// Journal returns recent log lines of NetworkManager and wpa_supplicant.
// With a zero since, the lines of the current boot are returned.
func (l *Linux) Journal(ctx context.Context, since time.Time, max int) ([]string, error) {
	args := []string{"--no-pager", "-q", "-o", "short-iso",
		"-u", "NetworkManager.service", "-u", "wpa_supplicant.service",
		"-n", strconv.Itoa(max)}
	if since.IsZero() {
		args = append(args, "-b")
	} else {
		args = append(args, "--since", since.Local().Format("2006-01-02 15:04:05"))
	}
	out, err := exec.CommandContext(ctx, "journalctl", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("journalctl: %w", err)
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" || strings.HasPrefix(line, "-- ") {
			continue
		}
		lines = append(lines, line)
	}
	return lines, nil
}

var reRegCountry = regexp.MustCompile(`(?m)^country ([A-Z0-9]{2}):`)

// WiFiCountry returns the configured WiFi country (regulatory domain) or "".
func (l *Linux) WiFiCountry(ctx context.Context) string {
	if path, err := exec.LookPath("raspi-config"); err == nil {
		out, err := exec.CommandContext(ctx, path, "nonint", "get_wifi_country").Output()
		if cc := strings.TrimSpace(string(out)); err == nil && ValidCountry(cc) {
			return cc
		}
	}
	out, err := exec.CommandContext(ctx, "iw", "reg", "get").Output()
	if err != nil {
		return ""
	}
	if m := reRegCountry.FindStringSubmatch(string(out)); m != nil && ValidCountry(m[1]) {
		return m[1]
	}
	return ""
}

// SetWiFiCountry sets the WiFi country permanently (via raspi-config when
// available) and unblocks the WiFi radio.
func (l *Linux) SetWiFiCountry(ctx context.Context, code string) error {
	if !ValidCountry(code) {
		return fmt.Errorf("invalid country code %q", code)
	}
	var cmd *exec.Cmd
	if path, err := exec.LookPath("raspi-config"); err == nil {
		cmd = exec.CommandContext(ctx, path, "nonint", "do_wifi_country", code)
	} else {
		cmd = exec.CommandContext(ctx, "iw", "reg", "set", code)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("setting WiFi country failed: %v %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.CommandContext(ctx, "rfkill", "unblock", "wifi").Run()
	return nil
}

// Reboot restarts the Raspberry Pi.
func (l *Linux) Reboot(ctx context.Context) error {
	return exec.CommandContext(ctx, "systemctl", "reboot").Run()
}

// PowerOff shuts the Raspberry Pi down.
func (l *Linux) PowerOff(ctx context.Context) error {
	return exec.CommandContext(ctx, "systemctl", "poweroff").Run()
}
