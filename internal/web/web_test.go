package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkramme/rpi-eap-bridge/internal/bridge"
	"github.com/pkramme/rpi-eap-bridge/internal/config"
	"github.com/pkramme/rpi-eap-bridge/internal/demo"
	"github.com/pkramme/rpi-eap-bridge/internal/diag"
)

func TestPolicy(t *testing.T) {
	lan := netip.MustParsePrefix("192.168.77.1/24")
	wifiNet := []netip.Prefix{netip.MustParsePrefix("172.20.10.0/28")}
	p := policy{LAN: lan, LANIf: "eth0", WiFiIf: "wlan0", WiFiPrefixes: wifiNet, Mode: "auto"}

	onEAP := p
	onEAP.UplinkManaged = true
	for addr, want := range map[string]bool{
		"127.0.0.1:5000":          true,
		"[::1]:5000":              true,
		"192.168.77.23:5000":      true,
		"[fe80::1%eth0]:5000":     true,
		"[fe80::1%wlan0]:5000":    false,
		"172.20.10.5:5000":        false,
		"10.0.0.5:5000":           false,
		"[2001:db8::1]:5000":      false,
		"[::ffff:192.168.77.9]:1": true,
		"garbage":                 false,
	} {
		if got := onEAP.allow(addr); got != want {
			t.Errorf("on enterprise WiFi: allow(%s) = %v, want %v", addr, got, want)
		}
	}
	// Setup over a phone hotspot: same local network is fine, the internet is not.
	for addr, want := range map[string]bool{
		"172.20.10.5:5000":     true,
		"[fe80::1%wlan0]:5000": true,
		"8.8.8.8:5000":         false,
		"[2001:db8::1]:5000":   false,
	} {
		if got := p.allow(addr); got != want {
			t.Errorf("on hotspot: allow(%s) = %v, want %v", addr, got, want)
		}
	}
	never := p
	never.Mode = "never"
	if never.allow("172.20.10.5:5000") {
		t.Error("mode never allowed the WiFi side")
	}
	always := onEAP
	always.Mode = "always"
	if !always.allow("172.20.10.5:5000") || always.allow("8.8.8.8:1") {
		t.Error("mode always")
	}
}

func TestHostAllowed(t *testing.T) {
	for host, want := range map[string]bool{
		"192.168.77.1": true, "192.168.77.1:8080": true, "[fe80::1%25eth0]": true, "[::1]:80": true,
		"localhost": true, "EAP-Bridge.local": true, "eap-bridge": true, "eap-bridge.local.": true,
		"router.example": true, "evil.example": false, "eap-bridge.evil.example": false,
	} {
		if got := hostAllowed(host, "eap-bridge", []string{"router.example"}); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	if !sameOrigin("same-origin", "", "x") || sameOrigin("cross-site", "", "x") {
		t.Error("Sec-Fetch-Site")
	}
	if !sameOrigin("", "http://192.168.77.1", "192.168.77.1") || sameOrigin("", "http://evil.example", "192.168.77.1") {
		t.Error("Origin")
	}
	// "null" carries no information; the CSRF token decides.
	if !sameOrigin("", "null", "192.168.77.1") {
		t.Error("Origin: null")
	}
	if !sameOrigin("", "", "x") {
		t.Error("no headers")
	}
}

func TestPBKDF2Vector(t *testing.T) {
	// RFC 7914, section 11
	got := hex.EncodeToString(pbkdf2([]byte("passwd"), []byte("salt"), 1, 64, sha256.New))
	want := "55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc49ca9cccf179b645991664b39d77ef317c71b845b1e30bd509112041d3a19783"
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestPasswordAndSession(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse") || VerifyPassword(h, "Correct horse") || VerifyPassword("garbage", "x") {
		t.Fatal("verify")
	}
	key := sessionKey("secret", h)
	now := time.Now()
	tok := makeSession(key, now.Add(time.Hour))
	if !checkSession(key, tok, now) || checkSession(key, tok, now.Add(2*time.Hour)) || checkSession(sessionKey("secret", "other"), tok, now) {
		t.Fatal("session")
	}
	if checkSession(key, tok+"x", now) {
		t.Fatal("tampered session accepted")
	}
}

func TestLimiter(t *testing.T) {
	var l limiter
	now := time.Now()
	for i := 0; i < 5; i++ {
		if l.wait(now) != 0 {
			t.Fatal("waiting too early")
		}
		l.fail(now)
	}
	if l.wait(now) == 0 {
		t.Fatal("no delay after 5 failures")
	}
	if l.wait(now.Add(time.Minute)) != 0 {
		t.Fatal("delay does not expire")
	}
}

// ---- HTTP tests with the simulated NetworkManager ----

type testServer struct {
	h     http.Handler
	store *config.Store
	mgr   *bridge.Manager
}

func newTestServer(t *testing.T) testServer {
	t.Helper()
	dir := t.TempDir()
	sim := demo.New()
	sim.Speed = 0.002
	store, err := config.Open(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := bridge.New(sim, sim, store, bridge.Options{
		ConnectionsDir: filepath.Join(dir, "conns"), DataDir: dir, Logger: logger,
		Probe: func(context.Context) []diag.Item { return []diag.Item{{Name: "x", Status: diag.OK}} },
	})
	mgr.DetectInterfaces(context.Background())
	srv, err := New(mgr, store, sim, Options{Version: "test", Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return testServer{srv.Handler(), store, mgr}
}

func (ts testServer) do(method, target, remote string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, target, body)
	r.RemoteAddr = remote
	r.Host = "192.168.77.1"
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	return w
}

const lanClient = "192.168.77.23:40000"

func getCSRF(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "eapb_csrf" {
			return c
		}
	}
	t.Fatal("no csrf cookie")
	return nil
}

func TestPagesRender(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{"/", "/wifi", "/settings", "/logs", "/fragment/status", "/api/status", "/static/style.css"} {
		w := ts.do("GET", path, lanClient, nil, nil)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d: %s", path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("GET %s: missing CSP", path)
		}
	}
	if w := ts.do("GET", "/", lanClient, nil, nil); !strings.Contains(w.Body.String(), "Set up WiFi") {
		t.Error("dashboard does not offer the WiFi setup")
	}
	if w := ts.do("GET", "/wifi", lanClient, nil, nil); !strings.Contains(w.Body.String(), `value="eduroam" checked`) {
		t.Error("eduroam is not preselected")
	}
}

func TestAccessAndHost(t *testing.T) {
	ts := newTestServer(t)
	// Before any status is known, the WiFi side is treated as the enterprise network.
	w := ts.do("GET", "/", "10.23.1.2:5555", nil, nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "connect via Ethernet") {
		t.Errorf("WiFi side: %d %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = lanClient
	r.Host = "evil.example"
	w = httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	if w.Code != http.StatusMisdirectedRequest {
		t.Errorf("foreign host: %d", w.Code)
	}
}

func TestCSRF(t *testing.T) {
	ts := newTestServer(t)
	first := ts.do("GET", "/", lanClient, nil, nil)
	c := getCSRF(t, first)

	if w := ts.do("POST", "/settings/radio", lanClient, url.Values{}, []*http.Cookie{c}); w.Code != http.StatusForbidden {
		t.Errorf("missing token: %d", w.Code)
	}
	if w := ts.do("POST", "/settings/radio", lanClient, url.Values{"csrf": {"x" + c.Value[1:]}}, []*http.Cookie{c}); w.Code != http.StatusForbidden {
		t.Errorf("wrong token: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "/settings/radio", strings.NewReader(url.Values{"csrf": {c.Value}}.Encode()))
	r.RemoteAddr, r.Host = lanClient, "192.168.77.1"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site: %d", w.Code)
	}
	if w := ts.do("POST", "/settings/radio", lanClient, url.Values{"csrf": {c.Value}}, []*http.Cookie{c}); w.Code != http.StatusSeeOther {
		t.Errorf("valid token: %d %s", w.Code, w.Body.String())
	}
}

// Regression: on plain http://192.168.77.1 browsers send no Sec-Fetch-Site
// header, and with a no-referrer policy they send "Origin: null". Saving the
// WiFi form was then rejected as "Request from another web site blocked."
func TestFormPostFromInsecureLANOrigin(t *testing.T) {
	ts := newTestServer(t)
	page := ts.do("GET", "/wifi", lanClient, nil, nil)
	if p := page.Header().Get("Referrer-Policy"); p == "no-referrer" || p == "" {
		t.Errorf("Referrer-Policy %q makes browsers send Origin: null", p)
	}
	c := getCSRF(t, page)
	form := url.Values{
		"csrf": {c.Value}, "ssid": {"eduroam"}, "eap": {"peap"}, "phase2_peap": {"mschapv2"}, "phase2_ttls": {"pap"},
		"identity": {"jane@uni.edu"}, "password": {"pw"}, "ca_mode": {"system"},
	}
	post := func(origin string, withToken bool) int {
		f := url.Values{}
		for k, v := range form {
			f[k] = v
		}
		if !withToken {
			f.Del("csrf")
		}
		r := httptest.NewRequest("POST", "/wifi", strings.NewReader(f.Encode()))
		r.RemoteAddr, r.Host = lanClient, "192.168.77.1"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		ts.h.ServeHTTP(w, r)
		return w.Code
	}
	if code := post("null", false); code != http.StatusForbidden {
		t.Errorf("Origin null without token: %d, want 403", code)
	}
	if code := post("http://evil.example", true); code != http.StatusForbidden {
		t.Errorf("foreign origin: %d, want 403", code)
	}
	if code := post("null", true); code != http.StatusSeeOther {
		t.Errorf("Origin null with token: %d, want 303", code)
	}
}

func TestWiFiFormValidationAndConnect(t *testing.T) {
	ts := newTestServer(t)
	c := getCSRF(t, ts.do("GET", "/wifi", lanClient, nil, nil))
	form := url.Values{
		"csrf": {c.Value}, "ssid": {"Uni-Guest"}, "eap": {"peap"}, "phase2_peap": {"mschapv2"}, "phase2_ttls": {"pap"},
		"identity": {"jane@uni.edu"}, "password": {"pw"}, "ca_mode": {"system"},
	}
	w := ts.do("POST", "/wifi", lanClient, form, []*http.Cookie{c})
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "does not use a username login") {
		t.Fatalf("open network accepted: %d", w.Code)
	}
	form.Set("ssid", "eduroam")
	w = ts.do("POST", "/wifi", lanClient, form, []*http.Cookie{c})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	if cfg := ts.store.Get(); cfg.WiFi == nil || cfg.WiFi.Domain != "uni.edu" {
		t.Fatalf("settings not saved: %+v", cfg.WiFi)
	}
	// The password never appears in any page.
	time.Sleep(50 * time.Millisecond)
	for _, p := range []string{"/", "/wifi", "/api/status", "/settings"} {
		if body := ts.do("GET", p, lanClient, nil, nil).Body.String(); strings.Contains(body, `"pw"`) || strings.Contains(body, `value="pw"`) {
			t.Errorf("%s leaks the password", p)
		}
	}
}

func TestLogin(t *testing.T) {
	ts := newTestServer(t)
	hash, _ := HashPassword("secret password")
	if err := ts.store.Update(func(c *config.Config) error { c.AdminPasswordHash = hash; return nil }); err != nil {
		t.Fatal(err)
	}
	w := ts.do("GET", "/settings", lanClient, nil, nil)
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/login") {
		t.Fatalf("not redirected to login: %d", w.Code)
	}
	if w := ts.do("GET", "/fragment/status", lanClient, nil, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("fragment without login: %d", w.Code)
	}
	c := getCSRF(t, ts.do("GET", "/login", lanClient, nil, nil))
	if w := ts.do("POST", "/login", lanClient, url.Values{"csrf": {c.Value}, "password": {"nope"}}, []*http.Cookie{c}); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", w.Code)
	}
	w = ts.do("POST", "/login", lanClient, url.Values{"csrf": {c.Value}, "password": {"secret password"}, "next": {"/settings"}}, []*http.Cookie{c})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings" {
		t.Fatalf("login: %d %s", w.Code, w.Header().Get("Location"))
	}
	var session *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "eapb_session" {
			session = ck
		}
	}
	if session == nil || !session.HttpOnly {
		t.Fatal("no session cookie")
	}
	if w := ts.do("GET", "/settings", lanClient, nil, []*http.Cookie{session}); w.Code != http.StatusOK {
		t.Errorf("with session: %d", w.Code)
	}
	if safeNext("//evil.example") != "/" || safeNext("/wifi") != "/wifi" {
		t.Error("safeNext")
	}
}
