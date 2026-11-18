// Package web implements the web interface of the bridge.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pkramme/rpi-eap-bridge/internal/bridge"
	"github.com/pkramme/rpi-eap-bridge/internal/config"
	"github.com/pkramme/rpi-eap-bridge/internal/diag"
	"github.com/pkramme/rpi-eap-bridge/internal/system"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

const (
	csrfCookie    = "eapb_csrf"
	sessionCookie = "eapb_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// Options configure the web server.
type Options struct {
	Version string
	Logger  *slog.Logger
	Demo    bool
}

// Server is the web interface.
type Server struct {
	mgr      *bridge.Manager
	cfg      *config.Store
	sys      system.System
	opt      Options
	log      *slog.Logger
	hostname string
	pages    map[string]*template.Template
	static   http.Handler
	limiter  limiter

	assetVersion string
}

type ctxKey int

const (
	csrfKey ctxKey = iota
	authedKey
)

var pageNames = []string{"dashboard", "wifi", "settings", "logs", "login", "message"}

// New creates the web server.
func New(mgr *bridge.Manager, cfg *config.Store, sys system.System, opt Options) (*Server, error) {
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	s := &Server{mgr: mgr, cfg: cfg, sys: sys, opt: opt, log: opt.Logger, hostname: sys.Hostname(), pages: map[string]*template.Template{}}
	for _, p := range pageNames {
		t, err := template.New(p).Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/partials.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", p, err)
		}
		s.pages[p] = t
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	s.static = http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	// Content hash for cache busting: browsers fetch new CSS/JS after updates.
	h := sha256.New()
	for _, name := range []string{"static/style.css", "static/app.js"} {
		data, err := staticFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		h.Write(data)
	}
	s.assetVersion = hex.EncodeToString(h.Sum(nil))[:12]
	return s, nil
}

// Handler returns the HTTP handler with all security layers.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /fragment/status", s.handleStatusFragment)
	mux.HandleFunc("GET /api/status", s.handleAPIStatus)
	mux.HandleFunc("GET /wifi", s.handleWiFiForm)
	mux.HandleFunc("POST /wifi", s.handleWiFiSubmit)
	mux.HandleFunc("POST /wifi/reconnect", s.handleReconnect)
	mux.HandleFunc("POST /wifi/disconnect", s.handleDisconnect)
	mux.HandleFunc("POST /wifi/forget", s.handleForget)
	mux.HandleFunc("POST /check", s.handleCheck)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings/password", s.handlePassword)
	mux.HandleFunc("POST /settings/country", s.handleCountry)
	mux.HandleFunc("POST /settings/radio", s.handleRadio)
	mux.HandleFunc("POST /system/reboot", s.handleReboot)
	mux.HandleFunc("POST /system/poweroff", s.handlePowerOff)
	mux.HandleFunc("GET /logs", s.handleLogs)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)

	var h http.Handler = mux
	h = s.requireAuth(h)
	h = s.csrfProtect(h)
	h = s.accessControl(h)
	h = s.withStatic(h)
	h = s.hostCheck(h)
	return securityHeaders(h)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// Not "no-referrer": with that policy browsers send "Origin: null" on
		// form submissions, which looks like a cross-site request.
		h.Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/static/") {
			h.Set("Cache-Control", "public, max-age=300")
		} else {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, s.hostname, s.cfg.Get().AllowedHosts) {
			http.Error(w, "Unknown host name. Open this page via its IP address (for example http://"+s.cfg.Get().LANIP()+") or http://"+s.hostname+".local.", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			s.static.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var deniedTmpl = template.Must(template.New("denied").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Not available here · EAP Bridge</title><link rel="stylesheet" href="/static/style.css"></head>
<body><main><section class="card">
<h1>Please connect via Ethernet</h1>
<p>For security, the settings of this bridge can only be opened from a device that is connected to the
Raspberry Pi's <strong>Ethernet port</strong> (directly or through the switch).</p>
<p>Connect your computer with a network cable and open <a href="http://{{.}}/">http://{{.}}</a>.</p>
</section></main></body></html>`))

func (s *Server) accessControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfg.Get()
		lan, _ := cfg.LANPrefix()
		ai := s.mgr.AccessInfo()
		p := policy{LAN: lan, LANIf: ai.LANIf, WiFiIf: ai.WiFiIf, WiFiPrefixes: ai.WiFiPrefixes, UplinkManaged: ai.UplinkManaged, Mode: cfg.UplinkAccess}
		if p.allow(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		s.log.Info("web access from the WiFi side refused", "remote", r.RemoteAddr)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_ = deniedTmpl.Execute(w, cfg.LANIP())
	})
}

func (s *Server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := ""
		if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) == 43 {
			tok = c.Value
		}
		if tok == "" {
			tok = randomToken()
			http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
			if !sameOrigin(r.Header.Get("Sec-Fetch-Site"), r.Header.Get("Origin"), r.Host) {
				http.Error(w, "Request from another web site blocked.", http.StatusForbidden)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(tok)) != 1 {
				http.Error(w, "This form has expired. Please go back, reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), csrfKey, tok)))
	})
}

func (s *Server) sessionValid(r *http.Request, cfg config.Config) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return checkSession(sessionKey(cfg.SessionSecret, cfg.AdminPasswordHash), c.Value, time.Now())
}

func (s *Server) setSession(w http.ResponseWriter, cfg config.Config) {
	exp := time.Now().Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: makeSession(sessionKey(cfg.SessionSecret, cfg.AdminPasswordHash), exp),
		Path: "/", Expires: exp, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfg.Get()
		if cfg.AdminPasswordHash == "" {
			next.ServeHTTP(w, r)
			return
		}
		if s.sessionValid(r, cfg) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authedKey, true)))
			return
		}
		if r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/fragment/") && !strings.HasPrefix(r.URL.Path, "/api/") {
			http.Redirect(w, r, "/login?next="+urlQueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		http.Error(w, "Please log in.", http.StatusUnauthorized)
	})
}

// pageData is passed to every template.
type pageData struct {
	Title, Nav        string
	CSRF              string
	Version, Hostname string
	AssetVersion      string
	LANIP             string
	Flash, FlashKind  string
	Authed            bool
	PasswordSet       bool
	Demo              bool

	Status bridge.Status
	Job    *bridge.Job
	Check  *bridge.CheckResult

	// WiFi setup
	Networks     []bridge.Network
	ScanError    string
	Form         wifiForm
	Errors       map[string]string
	FormError    string
	EAPChoices   []bridge.Choice
	PEAPChoices  []bridge.Choice
	TTLSChoices  []bridge.Choice
	HaveCAFile   bool
	CASubject    string
	SavedNetwork *config.WiFi

	// Settings, logs, login, messages
	Countries  []system.Country
	ConfigPath string
	Logs       []string
	LogError   string
	Next       string
	Message    string
	Link       string
	LinkText   string
}

type flash struct{ text, kind string }

var flashes = map[string]flash{
	"disconnected":     {"The WiFi connection was switched off. It stays off – also after a restart – until you click “Connect”.", "info"},
	"forgotten":        {"The WiFi settings were deleted.", "info"},
	"password-set":     {"The admin password was saved.", "ok"},
	"password-removed": {"The admin password was removed. Everyone on the Ethernet side can open this page now.", "info"},
	"country-set":      {"The WiFi country was saved.", "ok"},
	"radio-on":         {"WiFi was switched on.", "ok"},
	"logged-out":       {"You are logged out.", "info"},
	"busy":             {"A connection attempt is still running. Please wait until it has finished.", "warn"},
	"error":            {"That did not work. Please try again; the technical log may show details.", "bad"},
}

func (s *Server) base(r *http.Request, title, nav string) *pageData {
	cfg := s.cfg.Get()
	d := &pageData{
		Title: title, Nav: nav,
		Version: s.opt.Version, Hostname: s.hostname, LANIP: cfg.LANIP(), AssetVersion: s.assetVersion,
		PasswordSet: cfg.AdminPasswordHash != "", Demo: s.opt.Demo,
	}
	d.CSRF, _ = r.Context().Value(csrfKey).(string)
	d.Authed, _ = r.Context().Value(authedKey).(bool)
	if f, ok := flashes[r.URL.Query().Get("m")]; ok {
		d.Flash, d.FlashKind = f.text, f.kind
	}
	return d
}

func (s *Server) render(w http.ResponseWriter, status int, page string, d *pageData) {
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, "layout", d); err != nil {
		s.log.Error("rendering page failed", "page", page, "error", err)
		http.Error(w, "Internal error while rendering the page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) message(w http.ResponseWriter, r *http.Request, title, msg string) {
	d := s.base(r, title, "")
	d.Message = msg
	d.Link, d.LinkText = "/", "Back to the status page"
	s.render(w, http.StatusOK, "message", d)
}

var funcs = template.FuncMap{
	"bars": func(signal int) int {
		switch {
		case signal >= 75:
			return 4
		case signal >= 50:
			return 3
		case signal >= 25:
			return 2
		case signal > 0:
			return 1
		}
		return 0
	},
	"since": func(t time.Time) string {
		d := time.Since(t)
		switch {
		case d < 10*time.Second:
			return "just now"
		case d < time.Minute:
			return fmt.Sprintf("%d seconds ago", int(d.Seconds()))
		case d < 2*time.Minute:
			return "1 minute ago"
		case d < time.Hour:
			return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
		}
		return "at " + t.Local().Format("15:04")
	},
	"until": func(t time.Time) string {
		d := time.Until(t)
		switch {
		case d <= 0:
			return "now"
		case d < time.Minute:
			return "in less than a minute"
		case d < 2*time.Minute:
			return "in about 1 minute"
		}
		return fmt.Sprintf("in about %d minutes", int(d.Minutes()+0.5))
	},
	"icon": func(status string) string {
		switch status {
		case diag.OK:
			return "✓"
		case diag.Warn:
			return "!"
		case diag.Fail:
			return "✕"
		case diag.Info:
			return "i"
		}
		return "–"
	},
	"join": strings.Join,
}
