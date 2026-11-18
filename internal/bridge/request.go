package bridge

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/pkramme/rpi-eap-bridge/internal/config"
)

// Choice is an option of a select field.
type Choice struct {
	Value string
	Label string
}

// EAPChoices are the supported outer EAP methods.
var EAPChoices = []Choice{
	{"peap", "PEAP (most common)"},
	{"ttls", "TTLS"},
}

var phase2Choices = map[string][]Choice{
	"peap": {
		{"mschapv2", "MSCHAPv2 (most common)"},
		{"gtc", "GTC"},
		{"md5", "MD5"},
	},
	"ttls": {
		{"pap", "PAP (most common with TTLS)"},
		{"mschapv2", "MSCHAPv2"},
		{"mschap", "MSCHAP"},
		{"chap", "CHAP"},
		{"eap-mschapv2", "EAP-MSCHAPv2"},
		{"eap-gtc", "EAP-GTC"},
	},
}

// Phase2Choices returns the inner authentication methods for an EAP method.
func Phase2Choices(eap string) []Choice { return phase2Choices[eap] }

// DefaultPhase2 returns the most common inner method for an EAP method.
func DefaultPhase2(eap string) string {
	if eap == "ttls" {
		return "pap"
	}
	return "mschapv2"
}

func validPhase2(eap, phase2 string) bool {
	for _, c := range phase2Choices[eap] {
		if c.Value == phase2 {
			return true
		}
	}
	return false
}

// ConnectRequest holds the WiFi settings entered in the web interface.
type ConnectRequest struct {
	SSID              string
	Hidden            bool
	EAP               string
	Phase2            string
	Identity          string
	Password          string // empty = keep the saved password
	AnonymousIdentity string
	CAMode            string // "system", "file" or "none"
	Domain            string
	CACert            []byte // uploaded CA certificate (optional)
	CompatMode        bool   // switch off protected management frames (PMF)
}

var reDomain = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// Normalize cleans up user input. In "system" certificate mode an empty
// domain is derived from the realm of the username (jane@uni.edu → uni.edu),
// which is correct for most eduroam and company networks.
func (r *ConnectRequest) Normalize() {
	r.Identity = strings.TrimSpace(r.Identity)
	r.AnonymousIdentity = strings.TrimSpace(r.AnonymousIdentity)
	r.EAP = strings.ToLower(strings.TrimSpace(r.EAP))
	r.Phase2 = strings.ToLower(strings.TrimSpace(r.Phase2))
	r.CAMode = strings.ToLower(strings.TrimSpace(r.CAMode))
	d := strings.ToLower(strings.TrimSpace(r.Domain))
	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimPrefix(d, ".")
	r.Domain = strings.TrimSuffix(d, ".")
	if r.CAMode == "system" && r.Domain == "" {
		if _, realm, ok := strings.Cut(r.Identity, "@"); ok {
			r.Domain = strings.ToLower(realm)
		}
	}
	if r.CAMode == "none" {
		r.Domain = ""
	}
}

func hasControl(s string) bool {
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// Validate checks the request and returns messages per form field. saved is
// the currently saved network (or nil), haveCAFile whether a CA file exists.
func (r ConnectRequest) Validate(saved *config.WiFi, haveCAFile bool) map[string]string {
	errs := map[string]string{}
	switch {
	case r.SSID == "":
		errs["ssid"] = "Please choose a network or enter its name."
	case len(r.SSID) > 32:
		errs["ssid"] = "A WiFi network name can be at most 32 characters long."
	case !utf8.ValidString(r.SSID) || strings.ContainsRune(r.SSID, 0):
		errs["ssid"] = "This network name contains characters that are not supported."
	}
	if r.EAP != "peap" && r.EAP != "ttls" {
		errs["eap"] = "Please choose PEAP or TTLS."
	} else if !validPhase2(r.EAP, r.Phase2) {
		errs["phase2"] = "Please choose an inner authentication method."
	}
	switch {
	case r.Identity == "":
		errs["identity"] = "Please enter your username."
	case len(r.Identity) > 253 || hasControl(r.Identity) || !utf8.ValidString(r.Identity):
		errs["identity"] = "This username is not valid."
	}
	if len(r.AnonymousIdentity) > 253 || hasControl(r.AnonymousIdentity) || !utf8.ValidString(r.AnonymousIdentity) {
		errs["anonymous_identity"] = "This anonymous identity is not valid."
	}
	switch {
	case len(r.Password) > 256 || hasControl(r.Password) || !utf8.ValidString(r.Password):
		errs["password"] = "This password contains characters that are not supported."
	case r.Password == "" && saved == nil:
		errs["password"] = "Please enter your password."
	case r.Password == "" && saved.Identity != r.Identity:
		errs["password"] = "You changed the username – please enter the password for it."
	}
	switch r.CAMode {
	case "system":
		if r.Domain == "" {
			errs["domain"] = "Please enter the domain of your organisation's login server (e.g. university.edu), or choose another certificate option."
		}
	case "file":
		if len(r.CACert) == 0 && !haveCAFile {
			errs["ca_file"] = "Please choose the CA certificate file from your IT department."
		}
	case "none":
	default:
		errs["ca_mode"] = "Please choose how the server certificate should be checked."
	}
	if r.Domain != "" && (len(r.Domain) > 253 || !reDomain.MatchString(r.Domain)) {
		errs["domain"] = "This is not a valid domain name (example: university.edu)."
	}
	return errs
}

// profilePhase2 maps the form value to NetworkManager's phase2-auth and
// phase2-autheap properties.
func profilePhase2(eap, phase2 string) (auth, authEAP string) {
	if eap == "ttls" && strings.HasPrefix(phase2, "eap-") {
		return "", strings.TrimPrefix(phase2, "eap-")
	}
	return phase2, ""
}
