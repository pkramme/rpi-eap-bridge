package nm

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestSplitTerse(t *testing.T) {
	got := SplitTerse(`*:My\:WiFi:AA\:BB\:CC\:DD\:EE\:FF:2437 MHz:70:WPA2 802.1X`)
	want := []string{"*", "My:WiFi", "AA:BB:CC:DD:EE:FF", "2437 MHz", "70", "WPA2 802.1X"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := SplitTerse(`a\\b:`); !reflect.DeepEqual(got, []string{`a\b`, ""}) {
		t.Fatalf("backslash: got %q", got)
	}
}

func TestParseFields(t *testing.T) {
	out := "GENERAL.STATE:100 (connected)\nGENERAL.CONNECTION:eap-bridge-wifi\nGENERAL.HWADDR:DC\\:A6\\:32\\:01\\:02\\:03\n" +
		"IP4.ADDRESS[1]:10.1.2.3/16\nIP4.ADDRESS[2]:10.9.9.9/16\nIP4.GATEWAY:10.1.0.1\nIP4.DNS[1]:10.1.0.53\n"
	m := ParseFields(out)
	if m["GENERAL.HWADDR"][0] != "DC:A6:32:01:02:03" {
		t.Errorf("hwaddr = %q", m["GENERAL.HWADDR"])
	}
	if len(m["IP4.ADDRESS"]) != 2 || m["IP4.ADDRESS"][1] != "10.9.9.9/16" {
		t.Errorf("addresses = %q", m["IP4.ADDRESS"])
	}
	n, text := ParseState(m["GENERAL.STATE"][0])
	if n != 100 || text != "connected" {
		t.Errorf("state = %d %q", n, text)
	}
	n, text = ParseState("70 (connecting (getting IP configuration))")
	if n != 70 || text != "connecting (getting IP configuration)" {
		t.Errorf("state = %d %q", n, text)
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	for _, s := range []string{
		"simple", " leading", "trailing ", "  both  ", `back\slash`, "semi;colon#hash=eq",
		"tab\there", "new\nline", "ünïcødé €", "", " ",
	} {
		esc := EscapeString(s)
		if strings.ContainsAny(esc, "\n\r\t") {
			t.Errorf("escaped %q still contains control characters: %q", s, esc)
		}
		if esc != "" && (esc[0] == ' ' || esc[len(esc)-1] == ' ') {
			t.Errorf("escaped %q has unescaped outer space: %q", s, esc)
		}
		back, err := UnescapeString(esc)
		if err != nil || back != s {
			t.Errorf("round trip %q -> %q -> %q (%v)", s, esc, back, err)
		}
	}
	if _, err := UnescapeString(`bad\x`); err == nil {
		t.Error("invalid escape accepted")
	}
}

func TestByteList(t *testing.T) {
	if got := ByteList([]byte("edu")); got != "101;100;117;" {
		t.Fatalf("got %q", got)
	}
	b, ok := DecodeByteList("101;100;117;")
	if !ok || string(b) != "edu" {
		t.Fatalf("decode: %q %v", b, ok)
	}
	if _, ok := DecodeByteList("256;"); ok {
		t.Fatal("accepted 256")
	}
}

func TestEAPProfileKeyfile(t *testing.T) {
	p := EAPProfile{
		ID: "eap-bridge-wifi", UUID: "11111111-2222-4333-8444-555555555555", Interface: "wlan0",
		SSID: []byte("eduroam"), EAP: "peap", Phase2Auth: "mschapv2",
		Identity: "jane@uni.edu", AnonymousIdentity: "anonymous@uni.edu", Password: ` p\a;ss#word `,
		SystemCACerts: true, DomainSuffixMatch: "uni.edu", Autoconnect: true, Priority: 100,
	}
	data, err := p.Keyfile()
	if err != nil {
		t.Fatal(err)
	}
	kf, err := ParseKeyFile(data)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	checks := map[[2]string]string{
		{"connection", "type"}:            "wifi",
		{"connection", "interface-name"}:  "wlan0",
		{"wifi", "ssid"}:                  "101;100;117;114;111;97;109;",
		{"wifi-security", "key-mgmt"}:     "wpa-eap",
		{"802-1x", "eap"}:                 "peap;",
		{"802-1x", "identity"}:            "jane@uni.edu",
		{"802-1x", "password"}:            ` p\a;ss#word `,
		{"802-1x", "phase2-auth"}:         "mschapv2",
		{"802-1x", "system-ca-certs"}:     "true",
		{"802-1x", "domain-suffix-match"}: "uni.edu",
		{"ipv4", "method"}:                "auto",
		{"ipv4", "may-fail"}:              "false",
	}
	for k, want := range checks {
		if got := kf[k[0]][k[1]]; got != want {
			t.Errorf("%s.%s = %q, want %q", k[0], k[1], got, want)
		}
	}
	if _, ok := kf["802-1x"]["ca-cert"]; ok {
		t.Error("unexpected ca-cert")
	}

	p.EAP, p.Phase2Auth, p.Phase2AuthEAP = "ttls", "", "mschapv2"
	data, err = p.Keyfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "phase2-autheap=mschapv2\n") {
		t.Errorf("missing phase2-autheap:\n%s", data)
	}

	bad := p
	bad.Phase2AuthEAP = "pap"
	if _, err := bad.Keyfile(); err == nil {
		t.Error("accepted pap as EAP inner method")
	}
	bad = p
	bad.SSID = make([]byte, 33)
	if _, err := bad.Keyfile(); err == nil {
		t.Error("accepted 33 byte SSID")
	}
	bad = p
	bad.Password = "a\x00b"
	if _, err := bad.Keyfile(); err == nil {
		t.Error("accepted NUL in password")
	}
}

func TestLANProfileKeyfile(t *testing.T) {
	data, err := LANProfile{ID: "eap-bridge-lan", UUID: "u", Interface: "eth0", Address: netip.MustParsePrefix("192.168.77.1/24"), Priority: 100}.Keyfile()
	if err != nil {
		t.Fatal(err)
	}
	kf, err := ParseKeyFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if kf["ipv4"]["method"] != "shared" || kf["ipv4"]["address1"] != "192.168.77.1/24" || kf["connection"]["type"] != "ethernet" {
		t.Errorf("unexpected profile:\n%s", data)
	}
}

func TestDisablePMF(t *testing.T) {
	p := EAPProfile{ID: "x", UUID: "u", SSID: []byte("n"), EAP: "peap", Phase2Auth: "mschapv2", Identity: "i", Password: "p"}
	data, err := p.Keyfile()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "pmf=") {
		t.Errorf("pmf set by default:\n%s", data)
	}
	p.DisablePMF = true
	data, _ = p.Keyfile()
	kf, err := ParseKeyFile(data)
	if err != nil || kf["wifi-security"]["pmf"] != "1" || kf["wifi-security"]["key-mgmt"] != "wpa-eap" {
		t.Errorf("pmf not disabled (%v):\n%s", err, data)
	}
}

func TestErrorMessageDropsWarnings(t *testing.T) {
	// Real nmcli output from a Raspberry Pi.
	e := &CmdError{Stderr: "Warning: password for '802-1x.identity' not given in 'passwd-file' and nmcli cannot ask without '--ask' option.\n" +
		"Error: Connection activation failed: Secrets were required, but not provided\n" +
		"Hint: use 'journalctl -xe NM_CONNECTION=f794081d + NM_DEVICE=wlan0' to get more details.\n"}
	if got := e.Message(); got != "Connection activation failed: Secrets were required, but not provided" {
		t.Errorf("got %q", got)
	}
	if got := (&CmdError{Stderr: "something odd\n"}).Message(); got != "something odd" {
		t.Errorf("fallback: got %q", got)
	}
}
