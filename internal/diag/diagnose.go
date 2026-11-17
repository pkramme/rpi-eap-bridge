package diag

import (
	"fmt"
	"regexp"
	"strings"
)

// Diagnosis explains why a connection attempt failed.
type Diagnosis struct {
	// Kind is a machine readable category: auth, cert, method, notfound,
	// dhcp, timeout, assoc, tls, radio, nm or other.
	Kind     string
	Headline string
	Hints    []string
}

var (
	reProposed = regexp.MustCompile(`CTRL-EVENT-EAP-PROPOSED-METHOD vendor=0 method=(\d+)`)
	reSelected = regexp.MustCompile(`CTRL-EVENT-EAP-METHOD EAP vendor 0 method (\d+)`)
	rePeerCert = regexp.MustCompile(`CTRL-EVENT-EAP-PEER-CERT depth=0 subject='([^']*)'`)
	reCN       = regexp.MustCompile(`CN=([^/,]+)`)
)

// IANA EAP method numbers as logged by wpa_supplicant.
var eapNames = map[string]string{
	"4": "MD5", "6": "GTC", "13": "TLS", "21": "TTLS", "25": "PEAP",
	"26": "MSCHAPv2", "43": "FAST", "52": "PWD",
}

var eapNumbers = map[string]string{"peap": "25", "ttls": "21"}

// Diagnose analyses the nmcli error message and the logs written during a
// failed connection attempt. eap is the configured method ("peap"/"ttls").
func Diagnose(nmError string, logs []string, eap string) Diagnosis {
	text := nmError + "\n" + strings.Join(logs, "\n")
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(text, s) {
				return true
			}
		}
		return false
	}

	proposed := ""
	if m := reProposed.FindStringSubmatch(text); m != nil {
		proposed = m[1]
	}
	selected := map[string]bool{}
	for _, m := range reSelected.FindAllStringSubmatch(text, -1) {
		selected[m[1]] = true
	}
	want := eapNumbers[eap]

	var d Diagnosis
	switch {
	case has("NetworkManager is not running"):
		d = Diagnosis{Kind: "nm", Headline: "NetworkManager, the network service of the Raspberry Pi, is not running.",
			Hints: []string{"Restart the Raspberry Pi (Settings → Restart). If the problem stays, see the troubleshooting guide."}}

	case has("No suitable device found", "not available because", "Wi-Fi is disabled", "radio is disabled"):
		d = Diagnosis{Kind: "radio", Headline: "The WiFi adapter of the Raspberry Pi is switched off or not ready.",
			Hints: []string{"Open Settings, choose your country under “WiFi country” and save. Then switch WiFi on."}}

	case has("CTRL-EVENT-EAP-TLS-CERT-ERROR", "certificate verify failed", "Certificate verification failed",
		"Domain suffix mismatch", "Domain mismatch", "unable to get local issuer certificate",
		"self signed certificate", "self-signed certificate"):
		d = Diagnosis{Kind: "cert", Headline: "The login server of the network could not be verified (certificate check failed).",
			Hints: []string{
				"Check the “Server domain” in the security settings (for example university.edu). It has to match the certificate of the login server.",
				"If your IT department provides a CA certificate file, choose “Use a CA certificate file” and upload it.",
				"As a last resort choose “Don't check”. That works with almost every network, but is less secure.",
			}}

	case want != "" && proposed != "" && proposed != want && !selected[want]:
		name := eapNames[proposed]
		if name == "" {
			name = "method " + proposed
		}
		d = Diagnosis{Kind: "method", Headline: fmt.Sprintf("The network uses the login method %s, but %s is selected.", name, strings.ToUpper(eap))}
		if proposed == "13" {
			d.Hints = []string{"This network asks for a login with a personal certificate (EAP-TLS) instead of a password. This bridge only supports username and password."}
		} else {
			d.Hints = []string{fmt.Sprintf("Change “Method (EAP)” to %s in the WiFi settings and try again.", name)}
		}

	case has("CTRL-EVENT-EAP-FAILURE", "reason=AUTH_FAILED"):
		d = Diagnosis{Kind: "auth", Headline: "The network rejected the login: username or password are not accepted.",
			Hints: []string{
				"Check the username. Most organisations want the full form, e.g. jane.doe@university.edu – not just “jane.doe”.",
				"Check the password, including upper/lower case. Tick “Show password” to see what you typed.",
				"Check the inner authentication (Phase 2): PEAP almost always uses MSCHAPv2, TTLS often uses PAP.",
				"Some organisations use a separate WiFi password that differs from your normal account password.",
			}}

	case has("could not be found", "No network with SSID", "CTRL-EVENT-NETWORK-NOT-FOUND", "ssid-not-found"):
		d = Diagnosis{Kind: "notfound", Headline: "The WiFi network was not found.",
			Hints: []string{
				"Check the network name (exact spelling, upper/lower case).",
				"Move the Raspberry Pi closer to a window or an access point. Metal cases and USB 3 devices can weaken WiFi.",
				"The Raspberry Pi 3B, Zero W and Zero 2 W only support 2.4 GHz. If the network only uses 5 GHz, use a Pi 3B+, 4 or 5.",
			}}

	case has("IP configuration could not be reserved"):
		d = Diagnosis{Kind: "dhcp", Headline: "The login worked, but the network did not give the Raspberry Pi an IP address.",
			Hints: []string{
				"This is often a temporary network problem. The bridge keeps retrying automatically.",
				"Some networks only accept registered devices. The WiFi MAC address of this Pi is shown under Settings → About.",
			}}

	case has("CTRL-EVENT-ASSOC-REJECT"):
		d = Diagnosis{Kind: "assoc", Headline: "The access point refused the connection.",
			Hints: []string{"The access point may be full or the signal too weak. The bridge retries automatically; moving the Pi closer to an access point helps."}}

	case has("unsupported protocol", "wrong version number", "unsafe legacy renegotiation"):
		d = Diagnosis{Kind: "tls", Headline: "The login server of the network uses outdated encryption settings.",
			Hints: []string{"Current Raspberry Pi OS refuses very old encryption versions. Please contact your IT department and mention “outdated TLS version on the RADIUS server”."}}

	case has("took too long to authenticate", "supplicant-timeout", "association took too long"):
		d = Diagnosis{Kind: "timeout", Headline: "The login did not finish in time.",
			Hints: []string{
				"This is usually caused by a wrong security setting – check “Method (EAP)”, the inner authentication and the server certificate check.",
				"A weak WiFi signal can cause it, too. Move the Pi closer to an access point.",
				"If everything is right, the login server may be busy. The bridge keeps retrying automatically.",
			}}

	case has("Secrets were required"):
		d = Diagnosis{Kind: "auth", Headline: "The login was not accepted.",
			Hints: []string{
				"Check username and password (most organisations want the full form, e.g. jane.doe@university.edu).",
				"Check the security settings: “Method (EAP)”, the inner authentication and the server certificate check.",
			}}

	default:
		msg := strings.TrimSpace(nmError)
		if msg == "" {
			msg = "unknown error"
		}
		d = Diagnosis{Kind: "other", Headline: "The connection could not be established: " + msg,
			Hints: []string{"Check the settings and try again. The technical details below can help your IT department."}}
	}

	if server := serverName(text); server != "" && (d.Kind == "cert" || d.Kind == "auth" || d.Kind == "timeout") {
		d.Hints = append(d.Hints, fmt.Sprintf("The login server of the network identified itself as “%s”.", server))
	}
	return d
}

func serverName(text string) string {
	m := rePeerCert.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	if cn := reCN.FindStringSubmatch(m[1]); cn != nil {
		return strings.TrimSpace(cn[1])
	}
	return m[1]
}

// Relevant filters log lines that concern the WiFi connection and returns
// at most max of the most recent ones.
func Relevant(lines []string, iface string, max int) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, "wpa_supplicant") || (iface != "" && strings.Contains(l, iface)) || strings.Contains(l, "802-1x") {
			out = append(out, l)
		}
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}
