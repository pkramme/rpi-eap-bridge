// Package bridge contains the logic of the EAP bridge: it writes the
// NetworkManager profiles, connects to the enterprise WiFi, shares the
// connection on the Ethernet port, reconnects automatically and runs
// connection tests.
package bridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pkramme/rpi-eap-bridge/internal/config"
	"github.com/pkramme/rpi-eap-bridge/internal/diag"
	"github.com/pkramme/rpi-eap-bridge/internal/fsutil"
	"github.com/pkramme/rpi-eap-bridge/internal/nm"
	"github.com/pkramme/rpi-eap-bridge/internal/system"
)

// Names of the NetworkManager profiles managed by the bridge.
const (
	WiFiConnID = "eap-bridge-wifi"
	LANConnID  = "eap-bridge-lan"
	caFileName = "ca.pem"
)

// ErrBusy is returned while a connection attempt is running.
var ErrBusy = errors.New("a connection attempt is still running – please wait until it has finished")

// FieldError is a validation error that belongs to one form field.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Msg }

// Options configure a Manager.
type Options struct {
	// ConnectionsDir is NetworkManager's profile directory.
	ConnectionsDir string
	// DataDir stores the uploaded CA certificate.
	DataDir string
	Logger  *slog.Logger
	// Probe runs the internet checks (replaceable in tests).
	Probe func(ctx context.Context) []diag.Item
	// ActivateTimeout is how long NetworkManager may take to connect.
	ActivateTimeout time.Duration
	// TickInterval is the interval of the background loop.
	TickInterval time.Duration
}

// Job is a connection attempt (started by the user or automatically).
type Job struct {
	ID       int
	Kind     string // "connect", "reconnect" or "auto"
	SSID     string
	Running  bool
	Success  bool
	Step     string
	Started  time.Time
	Finished time.Time
	Headline string
	Hints    []string
	Details  []string
	Check    *CheckResult
}

func (j *Job) clone() *Job {
	c := *j
	c.Hints = slices.Clone(j.Hints)
	c.Details = slices.Clone(j.Details)
	if j.Check != nil {
		cc := j.Check.clone()
		c.Check = &cc
	}
	return &c
}

type retryState struct {
	failures int
	next     time.Time
}

// Back-off between automatic reconnect attempts. It starts slowly so that a
// wrong password does not lock the user's account.
var retryBackoff = []time.Duration{2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute}

// Manager is the central object of the bridge.
type Manager struct {
	nm  nm.NM
	sys system.System
	cfg *config.Store
	opt Options
	log *slog.Logger

	mu        sync.Mutex // guards the fields below
	wifiIf    string
	lanIf     string
	job       *Job
	jobSeq    int
	lastCheck *CheckResult
	status    Status
	country   string
	countryAt time.Time
	retry     retryState
	scan      []Network
	scanAt    time.Time
	lanUpBusy bool

	connectMu sync.Mutex // serializes Connect/Forget
	statusMu  sync.Mutex // serializes status refreshes
	checkMu   sync.Mutex // serializes connection tests
}

// New creates a Manager.
func New(n nm.NM, sys system.System, cfg *config.Store, opt Options) *Manager {
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	if opt.Probe == nil {
		opt.Probe = diag.Probe
	}
	if opt.ActivateTimeout == 0 {
		opt.ActivateTimeout = 60 * time.Second
	}
	if opt.TickInterval == 0 {
		opt.TickInterval = 10 * time.Second
	}
	if opt.ConnectionsDir == "" {
		opt.ConnectionsDir = "/etc/NetworkManager/system-connections"
	}
	if opt.DataDir == "" {
		opt.DataDir = filepath.Dir(config.DefaultPath)
	}
	return &Manager{nm: n, sys: sys, cfg: cfg, opt: opt, log: opt.Logger}
}

func (m *Manager) wifiProfilePath() string {
	return filepath.Join(m.opt.ConnectionsDir, WiFiConnID+".nmconnection")
}

func (m *Manager) lanProfilePath() string {
	return filepath.Join(m.opt.ConnectionsDir, LANConnID+".nmconnection")
}

func (m *Manager) caPath() string { return filepath.Join(m.opt.DataDir, caFileName) }

// HasCAFile reports whether an uploaded CA certificate is stored.
func (m *Manager) HasCAFile() bool {
	_, err := os.Stat(m.caPath())
	return err == nil
}

// Interfaces returns the WiFi and Ethernet interface names.
func (m *Manager) Interfaces() (wifi, lan string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wifiIf, m.lanIf
}

// DetectInterfaces finds the WiFi and Ethernet interfaces unless they are
// set in the configuration.
func (m *Manager) DetectInterfaces(ctx context.Context) {
	cfg := m.cfg.Get()
	wifi, lan := cfg.WiFiInterface, cfg.LANInterface
	if wifi == "" || lan == "" {
		devs, err := m.nm.Devices(ctx)
		if err != nil {
			m.log.Warn("could not list network devices", "error", nm.ErrorMessage(err))
		}
		for _, d := range devs {
			if wifi == "" && d.Type == "wifi" {
				wifi = d.Name
			}
			if lan == "" && d.Type == "ethernet" && d.Name == "eth0" {
				lan = d.Name
			}
		}
		for _, d := range devs {
			if lan == "" && d.Type == "ethernet" {
				lan = d.Name
			}
		}
	}
	m.mu.Lock()
	changed := m.wifiIf != wifi || m.lanIf != lan
	m.wifiIf, m.lanIf = wifi, lan
	m.mu.Unlock()
	if changed {
		m.log.Info("network interfaces", "wifi", wifi, "ethernet", lan)
	}
}

// Run is the background loop: it keeps the sharing profile in place,
// refreshes the status and reconnects the WiFi when needed.
func (m *Manager) Run(ctx context.Context) {
	m.startup(ctx)
	t := time.NewTicker(m.opt.TickInterval)
	defer t.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if w, l := m.Interfaces(); w == "" || l == "" {
			m.DetectInterfaces(ctx)
		}
		if n%6 == 0 {
			if err := m.EnsureLAN(ctx); err != nil {
				m.log.Warn("Ethernet sharing setup failed", "error", err)
			}
		}
		st := m.refreshStatus(ctx)
		m.watchdog(ctx, st)
	}
}

func (m *Manager) startup(ctx context.Context) {
	m.DetectInterfaces(ctx)
	if err := m.EnsureLAN(ctx); err != nil {
		m.log.Warn("Ethernet sharing setup failed", "error", err)
	}
	// Raspberry Pi OS keeps WiFi blocked until a country is set. If a
	// country is known, make sure the radio is on.
	if on, err := m.nm.WiFiEnabled(ctx); err == nil && !on {
		if cc := m.WiFiCountry(ctx, true); cc != "" {
			m.log.Info("switching WiFi radio on", "country", cc)
			if err := m.nm.SetWiFiEnabled(ctx, true); err != nil {
				m.log.Warn("could not switch WiFi on", "error", nm.ErrorMessage(err))
			}
		}
	}
	cfg := m.cfg.Get()
	if cfg.WiFi != nil && cfg.Paused {
		// Honour "Disconnect" across reboots.
		_ = m.nm.Down(ctx, cfg.WiFi.UUID)
	}
	m.refreshStatus(ctx)
}

// EnsureLAN writes the Ethernet sharing profile if it is missing or
// outdated and activates it when a cable is plugged in.
func (m *Manager) EnsureLAN(ctx context.Context) error {
	cfg := m.cfg.Get()
	_, lanIf := m.Interfaces()
	if !cfg.ManageLAN || lanIf == "" {
		return nil
	}
	prefix, err := cfg.LANPrefix()
	if err != nil {
		return err
	}
	data, err := nm.LANProfile{ID: LANConnID, UUID: cfg.LANUUID, Interface: lanIf, Address: prefix, Priority: 100}.Keyfile()
	if err != nil {
		return err
	}
	path := m.lanProfilePath()
	if old, _ := os.ReadFile(path); !bytes.Equal(old, data) {
		if err := fsutil.WriteFileAtomic(path, data, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		if err := m.nm.Load(ctx, path); err != nil {
			return fmt.Errorf("loading %s: %s", path, nm.ErrorMessage(err))
		}
		m.log.Info("Ethernet sharing profile written", "path", path, "interface", lanIf, "address", prefix.String())
	}
	info, err := m.nm.DeviceInfo(ctx, lanIf)
	if err != nil {
		return errors.New(nm.ErrorMessage(err))
	}
	// NetworkManager normally activates the profile by itself when a cable
	// is plugged in; this only helps if it did not.
	if info.State == nm.StateDisconnected && m.sys.Carrier(lanIf) {
		m.mu.Lock()
		busy := m.lanUpBusy
		m.lanUpBusy = true
		m.mu.Unlock()
		if !busy {
			go func() {
				defer func() { m.mu.Lock(); m.lanUpBusy = false; m.mu.Unlock() }()
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if err := m.nm.Up(ctx, cfg.LANUUID, lanIf, 45*time.Second); err != nil {
					m.log.Warn("could not start Ethernet sharing", "error", nm.ErrorMessage(err))
				}
				m.invalidateStatus()
			}()
		}
	}
	return nil
}

// Connect saves the WiFi settings and starts a connection attempt.
func (m *Manager) Connect(ctx context.Context, req ConnectRequest) error {
	m.connectMu.Lock()
	defer m.connectMu.Unlock()
	if m.jobRunning() {
		return ErrBusy
	}
	wifiIf, _ := m.Interfaces()
	if wifiIf == "" {
		return errors.New("no WiFi adapter was found on this Raspberry Pi")
	}
	cfg := m.cfg.Get()
	uuid := ""
	if cfg.WiFi != nil {
		uuid = cfg.WiFi.UUID
	}
	if uuid == "" {
		uuid = config.NewUUID()
	}

	password := req.Password
	if password == "" {
		if cfg.WiFi == nil {
			return &FieldError{"password", "Please enter your password."}
		}
		pw, err := m.nm.Secret(ctx, uuid, "802-1x.password")
		if err != nil || pw == "" {
			return &FieldError{"password", "The saved password could not be read. Please enter it again."}
		}
		password = pw
	}

	auth, authEAP := profilePhase2(req.EAP, req.Phase2)
	prof := nm.EAPProfile{
		ID: WiFiConnID, UUID: uuid, Interface: wifiIf,
		SSID: []byte(req.SSID), Hidden: req.Hidden,
		EAP: req.EAP, Phase2Auth: auth, Phase2AuthEAP: authEAP,
		Identity: req.Identity, AnonymousIdentity: req.AnonymousIdentity, Password: password,
		Autoconnect: true, Priority: 100,
	}
	caSubject := ""
	if cfg.WiFi != nil {
		caSubject = cfg.WiFi.CASubject
	}
	switch req.CAMode {
	case "system":
		prof.SystemCACerts = true
		prof.DomainSuffixMatch = req.Domain
	case "file":
		if len(req.CACert) > 0 {
			pemData, desc, err := NormalizeCACert(req.CACert)
			if err != nil {
				return &FieldError{"ca_file", "This file cannot be used: " + err.Error() + "."}
			}
			if err := fsutil.WriteFileAtomic(m.caPath(), pemData, 0o644); err != nil {
				return fmt.Errorf("saving the CA certificate: %w", err)
			}
			caSubject = desc
		} else if !m.HasCAFile() {
			return &FieldError{"ca_file", "Please choose the CA certificate file from your IT department."}
		}
		prof.CACertPath = m.caPath()
		prof.DomainSuffixMatch = req.Domain
	}

	data, err := prof.Keyfile()
	if err != nil {
		return err
	}
	path := m.wifiProfilePath()
	if err := fsutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("saving the WiFi profile: %w", err)
	}
	if err := m.nm.Load(ctx, path); err != nil {
		return fmt.Errorf("NetworkManager did not accept the WiFi profile: %s", nm.ErrorMessage(err))
	}
	if req.CAMode != "file" {
		caSubject = ""
	}
	err = m.cfg.Update(func(c *config.Config) error {
		c.WiFi = &config.WiFi{
			SSID: req.SSID, Hidden: req.Hidden, EAP: req.EAP, Phase2: req.Phase2,
			Identity: req.Identity, AnonymousIdentity: req.AnonymousIdentity,
			CAMode: req.CAMode, Domain: req.Domain, CASubject: caSubject, UUID: uuid,
		}
		c.Paused = false
		return nil
	})
	if err != nil {
		return fmt.Errorf("saving the configuration: %w", err)
	}
	m.log.Info("WiFi settings saved", "ssid", req.SSID, "eap", req.EAP, "phase2", req.Phase2, "ca_mode", req.CAMode)
	m.resetRetry()
	_, err = m.startJob("connect", req.SSID, uuid)
	return err
}

// Reconnect starts a new connection attempt with the saved settings.
func (m *Manager) Reconnect(ctx context.Context) error {
	cfg := m.cfg.Get()
	if cfg.WiFi == nil {
		return errors.New("no WiFi network is set up yet")
	}
	if cfg.Paused {
		if err := m.cfg.Update(func(c *config.Config) error { c.Paused = false; return nil }); err != nil {
			return err
		}
	}
	m.resetRetry()
	_, err := m.startJob("reconnect", cfg.WiFi.SSID, cfg.WiFi.UUID)
	return err
}

// Disconnect disconnects the WiFi and keeps it off (also after a reboot)
// until the user connects again.
func (m *Manager) Disconnect(ctx context.Context) error {
	cfg := m.cfg.Get()
	if cfg.WiFi == nil {
		return nil
	}
	if err := m.cfg.Update(func(c *config.Config) error { c.Paused = true; return nil }); err != nil {
		return err
	}
	if err := m.nm.Down(ctx, cfg.WiFi.UUID); err != nil && !strings.Contains(nm.ErrorMessage(err), "not an active connection") {
		m.log.Warn("disconnect failed", "error", nm.ErrorMessage(err))
	}
	m.invalidateStatus()
	return nil
}

// Forget deletes the WiFi profile, the CA certificate and the saved settings.
func (m *Manager) Forget(ctx context.Context) error {
	m.connectMu.Lock()
	defer m.connectMu.Unlock()
	if m.jobRunning() {
		return ErrBusy
	}
	cfg := m.cfg.Get()
	if cfg.WiFi != nil {
		_ = m.nm.Down(ctx, cfg.WiFi.UUID)
		if err := m.nm.Delete(ctx, cfg.WiFi.UUID); err != nil {
			m.log.Warn("deleting the WiFi profile failed", "error", nm.ErrorMessage(err))
		}
	}
	for _, p := range []string{m.wifiProfilePath(), m.caPath()} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			m.log.Warn("could not remove file", "path", p, "error", err)
		}
	}
	if err := m.cfg.Update(func(c *config.Config) error { c.WiFi = nil; c.Paused = false; return nil }); err != nil {
		return err
	}
	m.mu.Lock()
	m.job, m.lastCheck, m.retry = nil, nil, retryState{}
	m.mu.Unlock()
	m.invalidateStatus()
	m.log.Info("WiFi settings deleted")
	return nil
}

// EnableWiFi switches the WiFi radio on.
func (m *Manager) EnableWiFi(ctx context.Context) error {
	defer m.invalidateStatus()
	if err := m.nm.SetWiFiEnabled(ctx, true); err != nil {
		return errors.New(nm.ErrorMessage(err))
	}
	return nil
}

// SetWiFiCountry sets the WiFi country and switches the radio on.
func (m *Manager) SetWiFiCountry(ctx context.Context, code string) error {
	if err := m.sys.SetWiFiCountry(ctx, code); err != nil {
		return err
	}
	m.WiFiCountry(ctx, true)
	if err := m.nm.SetWiFiEnabled(ctx, true); err != nil {
		m.log.Warn("could not switch WiFi on", "error", nm.ErrorMessage(err))
	}
	m.invalidateStatus()
	m.log.Info("WiFi country set", "country", code)
	return nil
}

func (m *Manager) jobRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.job != nil && m.job.Running
}

func (m *Manager) resetRetry() {
	m.mu.Lock()
	m.retry = retryState{}
	m.mu.Unlock()
}

func (m *Manager) startJob(kind, ssid, uuid string) (*Job, error) {
	m.mu.Lock()
	if m.job != nil && m.job.Running {
		m.mu.Unlock()
		return nil, ErrBusy
	}
	m.jobSeq++
	j := &Job{ID: m.jobSeq, Kind: kind, SSID: ssid, Running: true, Started: time.Now(), Step: "Starting…"}
	m.job = j
	snapshot := j.clone()
	m.mu.Unlock()
	go m.runJob(j, uuid)
	return snapshot, nil
}

func (m *Manager) setStep(j *Job, step string) {
	m.mu.Lock()
	if j.Running {
		j.Step = step
	}
	m.mu.Unlock()
}

func stepText(state int) string {
	switch state {
	case nm.StatePrepare:
		return "Preparing the WiFi adapter…"
	case nm.StateConfig:
		return "Connecting to the network and logging in…"
	case nm.StateNeedAuth:
		return "Waiting for the login details…"
	case nm.StateIPConfig:
		return "Logged in – getting an IP address…"
	case nm.StateIPCheck, nm.StateSecondaries:
		return "Checking the connection…"
	case nm.StateActivated:
		return "Connected – finishing up…"
	default:
		return "Connecting…"
	}
}

func (m *Manager) runJob(j *Job, uuid string) {
	ctx, cancel := context.WithTimeout(context.Background(), m.opt.ActivateTimeout+60*time.Second)
	defer cancel()
	wifiIf, _ := m.Interfaces()

	monCtx, stopMonitor := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-monCtx.Done():
				return
			case <-t.C:
				if info, err := m.nm.DeviceInfo(monCtx, wifiIf); err == nil {
					m.setStep(j, stepText(info.State))
				}
			}
		}
	}()

	m.log.Info("connecting", "ssid", j.SSID, "kind", j.Kind)
	err := m.nm.Up(ctx, uuid, wifiIf, m.opt.ActivateTimeout)
	stopMonitor()
	m.invalidateStatus()

	if err != nil {
		msg := nm.ErrorMessage(err)
		logs, _ := m.sys.Journal(ctx, j.Started.Add(-2*time.Second), 400)
		eap := ""
		if cfg := m.cfg.Get(); cfg.WiFi != nil {
			eap = cfg.WiFi.EAP
		}
		d := diag.Diagnose(msg, logs, eap)
		details := append([]string{"nmcli: " + msg}, diag.Relevant(logs, wifiIf, 80)...)
		m.log.Warn("connection attempt failed", "ssid", j.SSID, "error", msg, "diagnosis", d.Kind)
		m.finishJob(j, false, d, details, nil)
		return
	}

	m.setStep(j, "Connected – testing the internet connection…")
	res := m.RunCheck(ctx)
	head := fmt.Sprintf("Connected to “%s”.", j.SSID)
	if res.Online {
		head += " The internet connection works."
	} else {
		head += " The connection test found a problem – see below."
	}
	m.log.Info("connected", "ssid", j.SSID, "internet", res.Online)
	m.finishJob(j, true, diag.Diagnosis{Headline: head}, nil, &res)
}

func (m *Manager) finishJob(j *Job, ok bool, d diag.Diagnosis, details []string, res *CheckResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.Running, j.Success, j.Finished = false, ok, time.Now()
	j.Step = ""
	j.Headline, j.Hints, j.Details, j.Check = d.Headline, d.Hints, details, res
	if ok {
		m.retry = retryState{}
		return
	}
	// Plan the next automatic attempt. A rejected password waits longest,
	// because repeated failures can lock the user's account.
	i := m.retry.failures
	if d.Kind == "auth" {
		i = len(retryBackoff) - 1
	}
	if i >= len(retryBackoff) {
		i = len(retryBackoff) - 1
	}
	m.retry.next = time.Now().Add(retryBackoff[i])
}

// watchdog reconnects the WiFi when it is down. NetworkManager reconnects
// by itself in most cases, but it gives up after a login failure (for
// example while the organisation's login server is down). The watchdog
// retries with a growing pause in between.
func (m *Manager) watchdog(ctx context.Context, st Status) {
	cfg := m.cfg.Get()
	if cfg.WiFi == nil || cfg.Paused || !st.WiFi.Present || !st.WiFi.RadioOn || st.NMError != "" {
		m.resetRetry()
		return
	}
	if m.jobRunning() {
		return
	}
	if st.WiFi.State == nm.StateActivated && st.WiFi.Managed {
		m.resetRetry()
		return
	}
	if nm.IsConnecting(st.WiFi.State) || st.WiFi.State == nm.StateDeactivating {
		return // NetworkManager is busy
	}
	now := time.Now()
	m.mu.Lock()
	if m.retry.next.IsZero() {
		// Give NetworkManager's own reconnect a head start.
		m.retry.next = now.Add(30 * time.Second)
		m.mu.Unlock()
		return
	}
	if now.Before(m.retry.next) {
		m.mu.Unlock()
		return
	}
	m.retry.failures++
	attempt := m.retry.failures
	// Placeholder until the attempt has finished (finishJob sets the real value).
	m.retry.next = now.Add(retryBackoff[0])
	m.mu.Unlock()

	if !cfg.WiFi.Hidden {
		wifiIf, _ := m.Interfaces()
		if aps, err := m.nm.WiFiList(ctx, wifiIf, false); err == nil {
			visible := false
			for _, ap := range aps {
				if ap.SSID == cfg.WiFi.SSID {
					visible = true
					break
				}
			}
			if !visible {
				m.log.Info("configured network is not in range, not reconnecting yet", "ssid", cfg.WiFi.SSID)
				return
			}
		}
	}
	m.log.Info("WiFi is not connected, reconnecting automatically", "ssid", cfg.WiFi.SSID, "attempt", attempt)
	if _, err := m.startJob("auto", cfg.WiFi.SSID, cfg.WiFi.UUID); err != nil {
		m.log.Warn("automatic reconnect not started", "error", err)
	}
}

// DisplayJob returns the job that should be shown on the status page.
func (m *Manager) DisplayJob() *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.job
	switch {
	case j == nil:
		return nil
	case j.Running:
		return j.clone()
	case time.Since(j.Finished) > 15*time.Minute:
		return nil
	case j.Kind == "auto" && j.Success:
		return nil
	case !j.Success && m.status.Online() && m.status.Time.After(j.Finished):
		return nil // it has recovered since
	}
	return j.clone()
}

// Logs returns the recent log lines of NetworkManager and wpa_supplicant.
func (m *Manager) Logs(ctx context.Context) ([]string, error) {
	return m.sys.Journal(ctx, time.Time{}, 300)
}
