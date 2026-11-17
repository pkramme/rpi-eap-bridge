package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkramme/rpi-eap-bridge/internal/bridge"
	"github.com/pkramme/rpi-eap-bridge/internal/config"
	"github.com/pkramme/rpi-eap-bridge/internal/system"
)

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

// ---- Status ----------------------------------------------------------------

func (s *Server) statusData(r *http.Request, d *pageData) {
	d.Status = s.mgr.Status(r.Context())
	d.Job = s.mgr.DisplayJob()
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	d := s.base(r, "Status", "status")
	s.statusData(r, d)
	d.Check = s.mgr.LastCheck()
	s.render(w, http.StatusOK, "dashboard", d)
}

func (s *Server) handleStatusFragment(w http.ResponseWriter, r *http.Request) {
	d := s.base(r, "", "")
	s.statusData(r, d)
	var buf bytes.Buffer
	if err := s.pages["dashboard"].ExecuteTemplate(&buf, "status", d); err != nil {
		s.log.Error("rendering status failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Status bridge.Status
		Job    *bridge.Job
		Check  *bridge.CheckResult
	}{s.mgr.Status(r.Context()), s.mgr.DisplayJob(), s.mgr.LastCheck()}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	s.mgr.RunCheck(ctx)
	http.Redirect(w, r, "/#check", http.StatusSeeOther)
}

func (s *Server) handleReconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Reconnect(r.Context()); err != nil {
		code := "error"
		if errors.Is(err, bridge.ErrBusy) {
			code = "busy"
		}
		s.log.Warn("reconnect failed", "error", err)
		http.Redirect(w, r, "/?m="+code, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Disconnect(r.Context()); err != nil {
		s.log.Warn("disconnect failed", "error", err)
		http.Redirect(w, r, "/?m=error", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?m=disconnected", http.StatusSeeOther)
}

func (s *Server) handleForget(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Forget(r.Context()); err != nil {
		code := "error"
		if errors.Is(err, bridge.ErrBusy) {
			code = "busy"
		}
		http.Redirect(w, r, "/settings?m="+code, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?m=forgotten", http.StatusSeeOther)
}

// ---- WiFi setup ------------------------------------------------------------

type wifiForm struct {
	SSID             string
	Other            bool
	OtherSSID        string
	EAP              string
	Phase2PEAP       string
	Phase2TTLS       string
	Identity         string
	Anonymous        string
	CAMode           string
	Domain           string
	HasSavedPassword bool
}

func formFromConfig(w *config.WiFi) wifiForm {
	f := wifiForm{EAP: "peap", Phase2PEAP: bridge.DefaultPhase2("peap"), Phase2TTLS: bridge.DefaultPhase2("ttls"), CAMode: "system"}
	if w == nil {
		return f
	}
	f.SSID, f.Identity, f.Anonymous = w.SSID, w.Identity, w.AnonymousIdentity
	f.EAP, f.CAMode, f.Domain = w.EAP, w.CAMode, w.Domain
	if w.EAP == "ttls" {
		f.Phase2TTLS = w.Phase2
	} else {
		f.Phase2PEAP = w.Phase2
	}
	if w.Hidden {
		f.Other, f.OtherSSID = true, w.SSID
	}
	f.HasSavedPassword = true
	return f
}

func (s *Server) wifiPage(r *http.Request, f wifiForm, rescan bool) *pageData {
	cfg := s.cfg.Get()
	d := s.base(r, "WiFi setup", "wifi")
	d.Status = s.mgr.Status(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	nets, err := s.mgr.Scan(ctx, rescan)
	if err != nil {
		d.ScanError = "Searching for WiFi networks failed: " + err.Error()
	}
	d.Networks = nets
	visible := func(ssid string) bool {
		for _, n := range nets {
			if n.SSID == ssid && n.Enterprise {
				return true
			}
		}
		return false
	}
	switch {
	case f.Other:
	case f.SSID != "" && !visible(f.SSID):
		f.Other, f.OtherSSID = true, f.SSID
	case f.SSID == "":
		var enterprise []string
		for _, n := range nets {
			if n.Enterprise {
				enterprise = append(enterprise, n.SSID)
			}
		}
		for _, n := range enterprise {
			if n == "eduroam" {
				f.SSID = n
			}
		}
		if f.SSID == "" && len(enterprise) == 1 {
			f.SSID = enterprise[0]
		}
	}
	d.Form = f
	d.EAPChoices = bridge.EAPChoices
	d.PEAPChoices = bridge.Phase2Choices("peap")
	d.TTLSChoices = bridge.Phase2Choices("ttls")
	d.HaveCAFile = s.mgr.HasCAFile()
	d.SavedNetwork = cfg.WiFi
	if cfg.WiFi != nil {
		d.CASubject = cfg.WiFi.CASubject
	}
	return d
}

func (s *Server) handleWiFiForm(w http.ResponseWriter, r *http.Request) {
	f := formFromConfig(s.cfg.Get().WiFi)
	s.render(w, http.StatusOK, "wifi", s.wifiPage(r, f, r.URL.Query().Get("rescan") == "1"))
}

func (s *Server) handleWiFiSubmit(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Get()
	f := wifiForm{
		SSID:             r.FormValue("ssid"),
		OtherSSID:        r.FormValue("ssid_other"),
		EAP:              r.FormValue("eap"),
		Phase2PEAP:       r.FormValue("phase2_peap"),
		Phase2TTLS:       r.FormValue("phase2_ttls"),
		Identity:         r.FormValue("identity"),
		Anonymous:        r.FormValue("anonymous_identity"),
		CAMode:           r.FormValue("ca_mode"),
		Domain:           r.FormValue("domain"),
		HasSavedPassword: cfg.WiFi != nil,
	}
	f.Other = f.SSID == "__other__"
	req := bridge.ConnectRequest{
		EAP: f.EAP, Identity: f.Identity, Password: r.FormValue("password"),
		AnonymousIdentity: f.Anonymous, CAMode: f.CAMode, Domain: f.Domain,
	}
	if f.Other {
		// A network that is not in the list may be hidden: let
		// NetworkManager search for it actively.
		req.SSID, req.Hidden = strings.TrimSpace(f.OtherSSID), true
	} else {
		req.SSID = f.SSID
	}
	if req.EAP == "ttls" {
		req.Phase2 = f.Phase2TTLS
	} else {
		req.Phase2 = f.Phase2PEAP
	}
	if file, _, err := r.FormFile("ca_file"); err == nil {
		data, err := io.ReadAll(io.LimitReader(file, 256<<10))
		file.Close()
		if err == nil {
			req.CACert = data
		}
	}
	req.Normalize()

	errs := req.Validate(cfg.WiFi, s.mgr.HasCAFile())
	if n, ok := s.mgr.KnownNetwork(req.SSID); ok && !n.Enterprise && errs["ssid"] == "" {
		errs["ssid"] = fmt.Sprintf("“%s” does not use a username login (%s). This bridge only supports WPA-Enterprise networks such as eduroam.", n.SSID, n.SecurityLabel())
	}
	formErr := ""
	if len(errs) == 0 {
		err := s.mgr.Connect(r.Context(), req)
		var fe *bridge.FieldError
		switch {
		case err == nil:
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		case errors.As(err, &fe):
			errs[fe.Field] = fe.Msg
		default:
			formErr = err.Error()
		}
	}
	d := s.wifiPage(r, f, false)
	d.Errors, d.FormError = errs, formErr
	s.render(w, http.StatusUnprocessableEntity, "wifi", d)
}

// ---- Settings --------------------------------------------------------------

func (s *Server) settingsPage(r *http.Request) *pageData {
	d := s.base(r, "Settings", "settings")
	d.Status = s.mgr.Status(r.Context())
	d.Countries = system.Countries()
	d.ConfigPath = s.cfg.Path()
	d.SavedNetwork = s.cfg.Get().WiFi
	return d
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "settings", s.settingsPage(r))
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Get()
	fail := func(msg string) {
		d := s.settingsPage(r)
		d.FormError = msg
		s.render(w, http.StatusUnprocessableEntity, "settings", d)
	}
	if cfg.AdminPasswordHash != "" {
		if wait := s.limiter.wait(time.Now()); wait > 0 {
			fail(fmt.Sprintf("Too many wrong attempts. Please wait %d seconds.", int(wait.Seconds())+1))
			return
		}
		if !VerifyPassword(cfg.AdminPasswordHash, r.FormValue("current")) {
			s.limiter.fail(time.Now())
			fail("The current password is not correct.")
			return
		}
		s.limiter.reset()
	}
	if r.FormValue("action") == "remove" {
		if err := s.cfg.Update(func(c *config.Config) error { c.AdminPasswordHash = ""; return nil }); err != nil {
			fail("Saving failed: " + err.Error())
			return
		}
		clearSession(w)
		http.Redirect(w, r, "/settings?m=password-removed", http.StatusSeeOther)
		return
	}
	pw := r.FormValue("new")
	switch {
	case len([]rune(pw)) < 8:
		fail("The new password must have at least 8 characters.")
		return
	case pw != r.FormValue("confirm"):
		fail("The two new passwords are not the same.")
		return
	}
	hash, err := HashPassword(pw)
	if err != nil {
		fail("Saving failed: " + err.Error())
		return
	}
	if err := s.cfg.Update(func(c *config.Config) error { c.AdminPasswordHash = hash; return nil }); err != nil {
		fail("Saving failed: " + err.Error())
		return
	}
	s.setSession(w, s.cfg.Get())
	http.Redirect(w, r, "/settings?m=password-set", http.StatusSeeOther)
}

func (s *Server) handleCountry(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.FormValue("country")))
	known := false
	for _, c := range system.Countries() {
		known = known || c.Code == code
	}
	if !known {
		d := s.settingsPage(r)
		d.FormError = "Please choose a country from the list."
		s.render(w, http.StatusUnprocessableEntity, "settings", d)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.mgr.SetWiFiCountry(ctx, code); err != nil {
		d := s.settingsPage(r)
		d.FormError = err.Error()
		s.render(w, http.StatusUnprocessableEntity, "settings", d)
		return
	}
	http.Redirect(w, r, "/settings?m=country-set#country", http.StatusSeeOther)
}

func (s *Server) handleRadio(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.EnableWiFi(r.Context()); err != nil {
		s.log.Warn("switching WiFi on failed", "error", err)
		http.Redirect(w, r, "/?m=error", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?m=radio-on", http.StatusSeeOther)
}

func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	s.message(w, r, "Restarting…", "The Raspberry Pi is restarting. This takes about a minute. Then reload this page.")
	s.powerAction("reboot", s.sys.Reboot)
}

func (s *Server) handlePowerOff(w http.ResponseWriter, r *http.Request) {
	s.message(w, r, "Shutting down…", "The Raspberry Pi is shutting down. Wait until the green light has stopped flickering (about 20 seconds) before you unplug the power.")
	s.powerAction("power off", s.sys.PowerOff)
}

func (s *Server) powerAction(name string, fn func(context.Context) error) {
	s.log.Info("system "+name+" requested via web interface", "demo", s.opt.Demo)
	if s.opt.Demo {
		return
	}
	go func() {
		time.Sleep(2 * time.Second) // let the response reach the browser
		if err := fn(context.Background()); err != nil {
			s.log.Error("system "+name+" failed", "error", err)
		}
	}()
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	d := s.base(r, "Technical log", "settings")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	logs, err := s.mgr.Logs(ctx)
	if err != nil {
		d.LogError = err.Error()
	}
	d.Logs = logs
	s.render(w, http.StatusOK, "logs", d)
}

// ---- Login -----------------------------------------------------------------

func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return "/"
	}
	return next
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Get().AdminPasswordHash == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	d := s.base(r, "Log in", "")
	d.Next = safeNext(r.URL.Query().Get("next"))
	s.render(w, http.StatusOK, "login", d)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Get()
	next := safeNext(r.FormValue("next"))
	if cfg.AdminPasswordHash == "" {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	fail := func(msg string) {
		d := s.base(r, "Log in", "")
		d.Next, d.FormError = next, msg
		s.render(w, http.StatusUnauthorized, "login", d)
	}
	if wait := s.limiter.wait(time.Now()); wait > 0 {
		fail(fmt.Sprintf("Too many wrong attempts. Please wait %d seconds.", int(wait.Seconds())+1))
		return
	}
	if !VerifyPassword(cfg.AdminPasswordHash, r.FormValue("password")) {
		s.limiter.fail(time.Now())
		s.log.Warn("wrong admin password", "remote", r.RemoteAddr)
		fail("Wrong password.")
		return
	}
	s.limiter.reset()
	s.setSession(w, cfg)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	if s.cfg.Get().AdminPasswordHash != "" {
		http.Redirect(w, r, "/login?m=logged-out", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
