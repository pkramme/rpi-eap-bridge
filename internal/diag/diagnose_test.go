package diag

import (
	"strings"
	"testing"
)

func TestDiagnose(t *testing.T) {
	tests := []struct {
		name string
		err  string
		logs []string
		eap  string
		kind string
		hint string
	}{
		{
			name: "rejected password",
			err:  "Connection activation failed: Secrets were required, but not provided",
			logs: []string{
				"wpa_supplicant[612]: wlan0: CTRL-EVENT-EAP-METHOD EAP vendor 0 method 25 (PEAP) selected",
				"wpa_supplicant[612]: wlan0: CTRL-EVENT-EAP-PEER-CERT depth=0 subject='/C=DE/O=Uni/CN=radius.uni.edu' hash=ab",
				"wpa_supplicant[612]: wlan0: CTRL-EVENT-EAP-FAILURE EAP authentication failed",
			},
			eap: "peap", kind: "auth", hint: "radius.uni.edu",
		},
		{
			name: "certificate",
			err:  "Connection activation failed: 802.1X supplicant took too long to authenticate",
			logs: []string{"wpa_supplicant[612]: wlan0: CTRL-EVENT-EAP-TLS-CERT-ERROR reason=1 depth=0 subject='/CN=radius.x.org' err='Domain suffix mismatch'"},
			eap:  "peap", kind: "cert",
		},
		{
			name: "method mismatch",
			err:  "Connection activation failed: Secrets were required, but not provided",
			logs: []string{
				"wpa_supplicant[612]: wlan0: CTRL-EVENT-EAP-PROPOSED-METHOD vendor=0 method=21",
				"wpa_supplicant[612]: wlan0: CTRL-EVENT-EAP-FAILURE EAP authentication failed",
			},
			eap: "peap", kind: "method", hint: "TTLS",
		},
		{
			name: "server proposes other method but ours is accepted",
			err:  "Connection activation failed: Secrets were required, but not provided",
			logs: []string{
				"CTRL-EVENT-EAP-PROPOSED-METHOD vendor=0 method=25",
				"CTRL-EVENT-EAP-METHOD EAP vendor 0 method 21 (TTLS) selected",
				"CTRL-EVENT-EAP-FAILURE EAP authentication failed",
			},
			eap: "ttls", kind: "auth",
		},
		{
			name: "certificate login", err: "x",
			logs: []string{"CTRL-EVENT-EAP-PROPOSED-METHOD vendor=0 method=13"},
			eap:  "peap", kind: "method", hint: "personal certificate",
		},
		{name: "not found", err: "Connection activation failed: The Wi-Fi network could not be found", kind: "notfound"},
		{name: "dhcp", err: "Connection activation failed: IP configuration could not be reserved (no available address, timeout, etc.)", kind: "dhcp"},
		{name: "timeout", err: "Connection activation failed: 802.1X supplicant took too long to authenticate", kind: "timeout"},
		{name: "radio", err: "Connection activation failed: No suitable device found for this connection (device wlan0 not available because device is not available).", kind: "radio"},
		{name: "nm down", err: "NetworkManager is not running.", kind: "nm"},
		{name: "other", err: "something strange", kind: "other", hint: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Diagnose(tt.err, tt.logs, tt.eap)
			if d.Kind != tt.kind {
				t.Fatalf("kind = %q, want %q (%s)", d.Kind, tt.kind, d.Headline)
			}
			if d.Headline == "" || len(d.Hints) == 0 {
				t.Fatalf("missing headline or hints: %+v", d)
			}
			all := d.Headline + " " + strings.Join(d.Hints, " ")
			if tt.hint != "" && !strings.Contains(all, tt.hint) {
				t.Errorf("expected %q in %q", tt.hint, all)
			}
		})
	}
}

func TestRelevant(t *testing.T) {
	lines := []string{"NetworkManager: device (eth0): x", "wpa_supplicant: wlan0: y", "NetworkManager: device (wlan0): z"}
	got := Relevant(lines, "wlan0", 1)
	if len(got) != 1 || !strings.HasSuffix(got[0], "z") {
		t.Fatalf("got %q", got)
	}
}
