package nm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CmdError is returned when nmcli exits with an error.
type CmdError struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *CmdError) Error() string {
	return fmt.Sprintf("nmcli %s: %s", strings.Join(e.Args, " "), e.Message())
}

// Message returns the human readable part of the error output.
// nmcli also prints "Warning:" and "Hint:" lines (for example a warning
// about secrets it cannot ask for interactively); only the "Error:" lines
// are returned when present.
func (e *CmdError) Message() string {
	var errs []string
	for _, l := range strings.Split(e.Stderr, "\n") {
		if msg, ok := strings.CutPrefix(strings.TrimSpace(l), "Error: "); ok {
			errs = append(errs, msg)
		}
	}
	if len(errs) > 0 {
		return strings.Join(errs, " ")
	}
	m := strings.TrimSpace(e.Stderr)
	if m == "" && e.Err != nil {
		m = e.Err.Error()
	}
	return m
}

func (e *CmdError) Unwrap() error { return e.Err }

// ErrorMessage returns the human readable part of an error from this package.
func ErrorMessage(err error) string {
	var ce *CmdError
	if errors.As(err, &ce) {
		return ce.Message()
	}
	return err.Error()
}

// Nmcli implements NM by running the nmcli command line tool.
type Nmcli struct {
	// Binary is the nmcli executable (default "nmcli").
	Binary string
}

var _ NM = (*Nmcli)(nil)

func (n *Nmcli) run(ctx context.Context, args ...string) (string, error) {
	bin := n.Binary
	if bin == "" {
		bin = "nmcli"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	// English messages (we match on some of them) but keep UTF-8 so that
	// network names with umlauts etc. survive.
	cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8", "LANG=C.UTF-8", "LANGUAGE=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &CmdError{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// Devices lists all network devices.
func (n *Nmcli) Devices(ctx context.Context) ([]Device, error) {
	out, err := n.run(ctx, "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status")
	if err != nil {
		return nil, err
	}
	var devs []Device
	for _, line := range splitLines(out) {
		f := SplitTerse(line)
		if len(f) < 4 {
			continue
		}
		devs = append(devs, Device{Name: f[0], Type: f[1], State: f[2], Connection: blank(f[3])})
	}
	return devs, nil
}

// DeviceInfo returns state and IPv4 details of one device.
func (n *Nmcli) DeviceInfo(ctx context.Context, iface string) (DeviceInfo, error) {
	// Request whole sections: individual field names differ slightly
	// between NetworkManager versions, sections do not.
	out, err := n.run(ctx, "-t", "-f", "GENERAL,IP4", "device", "show", iface)
	if err != nil {
		return DeviceInfo{}, err
	}
	m := ParseFields(out)
	first := func(key string) string {
		if v := m[key]; len(v) > 0 {
			return blank(v[0])
		}
		return ""
	}
	info := DeviceInfo{Name: iface}
	info.State, info.StateText = ParseState(first("GENERAL.STATE"))
	info.Reason = first("GENERAL.REASON")
	info.Connection = first("GENERAL.CONNECTION")
	info.ConnUUID = first("GENERAL.CON-UUID")
	info.HWAddr = first("GENERAL.HWADDR")
	for _, a := range m["IP4.ADDRESS"] {
		if a = blank(a); a != "" {
			info.IPv4 = append(info.IPv4, a)
		}
	}
	info.Gateway = first("IP4.GATEWAY")
	for _, d := range m["IP4.DNS"] {
		if d = blank(d); d != "" {
			info.DNS = append(info.DNS, d)
		}
	}
	return info, nil
}

// WiFiList lists visible access points. With rescan, NetworkManager scans
// first and waits for the results.
func (n *Nmcli) WiFiList(ctx context.Context, iface string, rescan bool) ([]AccessPoint, error) {
	r := "no"
	if rescan {
		r = "yes"
	}
	args := []string{"-t", "-f", "IN-USE,SSID,BSSID,FREQ,SIGNAL,SECURITY", "device", "wifi", "list", "--rescan", r}
	if iface != "" {
		args = append(args, "ifname", iface)
	}
	out, err := n.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var aps []AccessPoint
	for _, line := range splitLines(out) {
		f := SplitTerse(line)
		if len(f) < 6 {
			continue
		}
		freq, _ := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(f[3], "MHz")))
		sig, _ := strconv.Atoi(strings.TrimSpace(f[4]))
		aps = append(aps, AccessPoint{
			InUse:    strings.TrimSpace(f[0]) == "*",
			SSID:     f[1],
			BSSID:    f[2],
			FreqMHz:  freq,
			Signal:   sig,
			Security: strings.TrimSpace(blank(f[5])),
		})
	}
	return aps, nil
}

// Connections lists all connection profiles.
func (n *Nmcli) Connections(ctx context.Context) ([]Connection, error) {
	out, err := n.run(ctx, "-t", "-f", "NAME,UUID,TYPE,DEVICE,ACTIVE", "connection", "show")
	if err != nil {
		return nil, err
	}
	var conns []Connection
	for _, line := range splitLines(out) {
		f := SplitTerse(line)
		if len(f) < 5 {
			continue
		}
		conns = append(conns, Connection{Name: f[0], UUID: f[1], Type: f[2], Device: blank(f[3]), Active: f[4] == "yes"})
	}
	return conns, nil
}

// Load makes NetworkManager (re)read a keyfile.
func (n *Nmcli) Load(ctx context.Context, path string) error {
	_, err := n.run(ctx, "connection", "load", path)
	return err
}

// Up activates a connection and waits until it is active or has failed.
func (n *Nmcli) Up(ctx context.Context, uuid, iface string, wait time.Duration) error {
	args := []string{"-w", strconv.Itoa(int(wait.Seconds())), "connection", "up", "uuid", uuid}
	if iface != "" {
		args = append(args, "ifname", iface)
	}
	_, err := n.run(ctx, args...)
	return err
}

// Down deactivates a connection.
func (n *Nmcli) Down(ctx context.Context, uuid string) error {
	_, err := n.run(ctx, "connection", "down", "uuid", uuid)
	return err
}

// Delete removes a connection profile (including its file).
func (n *Nmcli) Delete(ctx context.Context, uuid string) error {
	_, err := n.run(ctx, "connection", "delete", "uuid", uuid)
	return err
}

// Secret returns a stored secret of a profile, e.g. "802-1x.password".
func (n *Nmcli) Secret(ctx context.Context, uuid, key string) (string, error) {
	out, err := n.run(ctx, "-e", "no", "-s", "-g", key, "connection", "show", "uuid", uuid)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(out, "\n"), nil
}

// WiFiEnabled reports whether the WiFi radio is switched on.
func (n *Nmcli) WiFiEnabled(ctx context.Context) (bool, error) {
	out, err := n.run(ctx, "radio", "wifi")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "enabled", nil
}

// SetWiFiEnabled switches the WiFi radio on or off.
func (n *Nmcli) SetWiFiEnabled(ctx context.Context, on bool) error {
	state := "off"
	if on {
		state = "on"
	}
	_, err := n.run(ctx, "radio", "wifi", state)
	return err
}
