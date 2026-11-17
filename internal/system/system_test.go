package system

import (
	"testing"
	"time"
)

func TestParseLeases(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	data := []byte(`1700003600 aa:bb:cc:dd:ee:01 192.168.77.100 laptop 01:aa:bb:cc:dd:ee:01
1600000000 aa:bb:cc:dd:ee:02 192.168.77.50 expired *
0 aa:bb:cc:dd:ee:03 192.168.77.9 * *
garbage line
`)
	got := ParseLeases(data, now)
	if len(got) != 2 {
		t.Fatalf("got %d leases: %+v", len(got), got)
	}
	if got[0].IP != "192.168.77.9" || got[0].Hostname != "" || !got[0].Expires.IsZero() {
		t.Errorf("first lease: %+v", got[0])
	}
	if got[1].Hostname != "laptop" || got[1].MAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("second lease: %+v", got[1])
	}
}

func TestValidCountry(t *testing.T) {
	for code, want := range map[string]bool{"DE": true, "US": true, "00": false, "de": false, "DEU": false, "": false} {
		if ValidCountry(code) != want {
			t.Errorf("ValidCountry(%q) != %v", code, want)
		}
	}
}
