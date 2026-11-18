package bridge_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkramme/rpi-eap-bridge/internal/bridge"
	"github.com/pkramme/rpi-eap-bridge/internal/config"
	"github.com/pkramme/rpi-eap-bridge/internal/demo"
	"github.com/pkramme/rpi-eap-bridge/internal/diag"
	"github.com/pkramme/rpi-eap-bridge/internal/nm"
)

func TestValidate(t *testing.T) {
	base := bridge.ConnectRequest{SSID: "eduroam", EAP: "peap", Phase2: "mschapv2", Identity: " jane@Uni.EDU ", Password: "pw", CAMode: "system"}

	r := base
	r.Normalize()
	if r.Identity != "jane@Uni.EDU" || r.Domain != "uni.edu" {
		t.Fatalf("normalize: %+v", r)
	}
	if errs := r.Validate(nil, false); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	cases := map[string]func(*bridge.ConnectRequest){
		"ssid":     func(r *bridge.ConnectRequest) { r.SSID = "" },
		"eap":      func(r *bridge.ConnectRequest) { r.EAP = "tls" },
		"phase2":   func(r *bridge.ConnectRequest) { r.Phase2 = "pap" }, // PAP is not valid inside PEAP
		"identity": func(r *bridge.ConnectRequest) { r.Identity = "" },
		"password": func(r *bridge.ConnectRequest) { r.Password = "" },
		"domain":   func(r *bridge.ConnectRequest) { r.Identity = "jane"; r.Domain = "" },
		"ca_file":  func(r *bridge.ConnectRequest) { r.CAMode = "file" },
		"ca_mode":  func(r *bridge.ConnectRequest) { r.CAMode = "maybe" },
	}
	for field, mutate := range cases {
		r := base
		mutate(&r)
		r.Normalize()
		if errs := r.Validate(nil, false); errs[field] == "" {
			t.Errorf("%s: expected an error, got %v", field, errs)
		}
	}

	// A saved password may be kept, unless the username changed.
	saved := &config.WiFi{Identity: "jane@Uni.EDU"}
	r = base
	r.Password = ""
	r.Normalize()
	if errs := r.Validate(saved, false); len(errs) != 0 {
		t.Errorf("keep password: %v", errs)
	}
	saved.Identity = "john@uni.edu"
	if errs := r.Validate(saved, false); errs["password"] == "" {
		t.Error("changed username without password accepted")
	}

	// TTLS with EAP inner method; certificate check off clears the domain.
	r = base
	r.EAP, r.Phase2, r.CAMode, r.Domain = "ttls", "eap-mschapv2", "none", "x.org"
	r.Normalize()
	if errs := r.Validate(nil, false); len(errs) != 0 || r.Domain != "" {
		t.Errorf("ttls: %v %+v", errs, r)
	}
}

func TestGroupNetworks(t *testing.T) {
	nets := bridge.GroupNetworks([]nm.AccessPoint{
		{SSID: "Home", FreqMHz: 2412, Signal: 90, Security: "WPA2"},
		{SSID: "eduroam", FreqMHz: 2437, Signal: 40, Security: "WPA2 802.1X"},
		{SSID: "eduroam", FreqMHz: 5180, Signal: 70, Security: "WPA2 802.1X", InUse: true},
		{SSID: "", FreqMHz: 2412, Signal: 99, Security: "WPA2 802.1X"},
		{SSID: "Corp", FreqMHz: 5500, Signal: 20, Security: "WPA3 802.1X"},
	})
	if len(nets) != 3 {
		t.Fatalf("got %d networks: %+v", len(nets), nets)
	}
	if nets[0].SSID != "eduroam" || nets[0].Signal != 70 || !nets[0].InUse || strings.Join(nets[0].Bands, ",") != "2.4 GHz,5 GHz" {
		t.Errorf("first: %+v", nets[0])
	}
	if nets[1].SSID != "Corp" || nets[2].SSID != "Home" || nets[2].Enterprise {
		t.Errorf("order: %+v", nets)
	}
}

func selfSigned(t *testing.T) []byte {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestNormalizeCACert(t *testing.T) {
	der := selfSigned(t)
	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	for name, in := range map[string][]byte{"pem": pemData, "der": der} {
		out, desc, err := bridge.NormalizeCACert(in)
		if err != nil || desc != "Test Root CA" || !strings.HasPrefix(string(out), "-----BEGIN CERTIFICATE-----") {
			t.Errorf("%s: %v %q", name, err, desc)
		}
	}
	if _, _, err := bridge.NormalizeCACert([]byte("hello")); err == nil {
		t.Error("garbage accepted")
	}
}

type env struct {
	m     *bridge.Manager
	sim   *demo.Demo
	store *config.Store
	dir   string
}

func newEnv(t *testing.T) env {
	t.Helper()
	dir := t.TempDir()
	sim := demo.New()
	sim.Speed = 0.002
	store, err := config.Open(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := bridge.New(sim, sim, store, bridge.Options{
		ConnectionsDir: filepath.Join(dir, "conns"),
		DataDir:        dir,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Probe: func(context.Context) []diag.Item {
			return []diag.Item{{Name: "Internet reachable", Status: diag.OK}}
		},
	})
	ctx := context.Background()
	m.DetectInterfaces(ctx)
	if w, l := m.Interfaces(); w != "wlan0" || l != "eth0" {
		t.Fatalf("interfaces %q %q", w, l)
	}
	if err := m.EnsureLAN(ctx); err != nil {
		t.Fatal(err)
	}
	return env{m, sim, store, dir}
}

func waitJob(t *testing.T, m *bridge.Manager) *bridge.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j := m.DisplayJob(); j != nil && !j.Running {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return nil
}

func TestConnectFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// The LAN profile was written and activated.
	lanFile := filepath.Join(e.dir, "conns", "eap-bridge-lan.nmconnection")
	if fi, err := os.Stat(lanFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("LAN profile: %v %v", fi, err)
	}

	// Wrong password: failure with a diagnosis and a long retry pause.
	req := bridge.ConnectRequest{SSID: "eduroam", EAP: "peap", Phase2: "mschapv2", Identity: "jane@uni.edu", Password: "wrong", CAMode: "system"}
	req.Normalize()
	if err := e.m.Connect(ctx, req); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, e.m)
	if j.Success || !strings.Contains(j.Headline, "rejected") || len(j.Details) == 0 {
		t.Fatalf("expected rejected login, got %+v", j)
	}
	st := e.m.Status(ctx)
	if st.Online() || time.Until(st.NextRetry) < 20*time.Minute {
		t.Errorf("expected a long retry pause, next retry %v", st.NextRetry)
	}

	// Correct password: success, profile is private and contains the settings.
	req.Password = "correct horse"
	if err := e.m.Connect(ctx, req); err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, e.m)
	if !j.Success || j.Check == nil || !j.Check.Online || !j.Check.Sharing {
		t.Fatalf("expected success, got %+v / %+v", j, j.Check)
	}
	wifiFile := filepath.Join(e.dir, "conns", "eap-bridge-wifi.nmconnection")
	fi, err := os.Stat(wifiFile)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("WiFi profile: %v %v", fi, err)
	}
	data, _ := os.ReadFile(wifiFile)
	for _, want := range []string{"domain-suffix-match=uni.edu", "system-ca-certs=true", "password=correct horse"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("profile lacks %q", want)
		}
	}
	if st := e.m.Status(ctx); !st.Online() || !st.LAN.Active() || len(st.LAN.Clients) == 0 {
		t.Errorf("status not online: %+v", st)
	}

	// Re-saving without a password keeps the stored one.
	req.Password = ""
	if err := e.m.Connect(ctx, req); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, e.m); !j.Success {
		t.Fatalf("keeping the password failed: %+v", j)
	}

	// Disconnect pauses, Forget removes everything.
	if err := e.m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if st := e.m.Status(ctx); st.Online() || !st.Paused {
		t.Errorf("still online after disconnect: %+v", st)
	}
	if err := e.m.Forget(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wifiFile); !os.IsNotExist(err) {
		t.Errorf("profile still exists: %v", err)
	}
	if e.store.Get().WiFi != nil {
		t.Error("settings still saved")
	}
}

func TestConnectWithCAFile(t *testing.T) {
	e := newEnv(t)
	req := bridge.ConnectRequest{SSID: "eduroam", EAP: "ttls", Phase2: "pap", Identity: "jane@uni.edu", Password: "pw", CAMode: "file", CACert: selfSigned(t)}
	req.Normalize()
	if err := e.m.Connect(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	waitJob(t, e.m)
	ca, err := os.ReadFile(filepath.Join(e.dir, "ca.pem"))
	if err != nil || !strings.HasPrefix(string(ca), "-----BEGIN CERTIFICATE-----") {
		t.Fatalf("CA file: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(e.dir, "conns", "eap-bridge-wifi.nmconnection"))
	if !strings.Contains(string(data), "ca-cert="+filepath.Join(e.dir, "ca.pem")) || !strings.Contains(string(data), "phase2-auth=pap") {
		t.Errorf("profile:\n%s", data)
	}
	if e.store.Get().WiFi.CASubject != "Test Root CA" {
		t.Errorf("CA subject not saved: %+v", e.store.Get().WiFi)
	}
}

func TestMethodMismatchDiagnosis(t *testing.T) {
	e := newEnv(t)
	req := bridge.ConnectRequest{SSID: "CorpNet", EAP: "ttls", Phase2: "pap", Identity: "jane@corp.example", Password: "pw", CAMode: "none"}
	req.Normalize()
	if err := e.m.Connect(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, e.m); j.Success || !strings.Contains(j.Headline, "PEAP") {
		t.Fatalf("expected a method hint, got %+v", j)
	}
}

func TestCompatModeRetry(t *testing.T) {
	e := newEnv(t)
	profile := filepath.Join(e.dir, "conns", "eap-bridge-wifi.nmconnection")

	// LegacyNet can only be joined with PMF off: the bridge retries by itself
	// and keeps compatibility mode.
	req := bridge.ConnectRequest{SSID: "LegacyNet", EAP: "peap", Phase2: "mschapv2", Identity: "jane@uni.edu", Password: "pw", CAMode: "none"}
	req.Normalize()
	if err := e.m.Connect(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, e.m); !j.Success {
		t.Fatalf("expected success in compatibility mode, got %+v", j)
	}
	if !e.store.Get().WiFi.CompatMode {
		t.Error("compatibility mode not saved")
	}
	if data, _ := os.ReadFile(profile); !strings.Contains(string(data), "pmf=1") {
		t.Errorf("profile without pmf=1:\n%s", data)
	}

	// WPA3-Only can never be joined: clear diagnosis, normal profile restored.
	req.SSID = "WPA3-Only"
	if err := e.m.Connect(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, e.m)
	if j.Success || !strings.Contains(j.Headline, "could not join") {
		t.Fatalf("expected association failure, got %+v", j)
	}
	if e.store.Get().WiFi.CompatMode {
		t.Error("compatibility mode kept although it did not help")
	}
	if data, _ := os.ReadFile(profile); strings.Contains(string(data), "pmf=") {
		t.Errorf("profile not restored:\n%s", data)
	}
}
