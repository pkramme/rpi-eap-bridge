package system

import (
	"bufio"
	"bytes"
	"os"
	"sort"
	"strings"
)

// Country is an ISO 3166 country.
type Country struct {
	Code string
	Name string
}

// fallbackCountries is used when the system's iso3166.tab is missing.
var fallbackCountries = []Country{
	{"AR", "Argentina"}, {"AT", "Austria"}, {"AU", "Australia"}, {"BE", "Belgium"},
	{"BR", "Brazil"}, {"CA", "Canada"}, {"CH", "Switzerland"}, {"CL", "Chile"},
	{"CN", "China"}, {"CZ", "Czech Republic"}, {"DE", "Germany"}, {"DK", "Denmark"},
	{"EE", "Estonia"}, {"ES", "Spain"}, {"FI", "Finland"}, {"FR", "France"},
	{"GB", "United Kingdom"}, {"GR", "Greece"}, {"HK", "Hong Kong"}, {"HR", "Croatia"},
	{"HU", "Hungary"}, {"IE", "Ireland"}, {"IL", "Israel"}, {"IN", "India"},
	{"IS", "Iceland"}, {"IT", "Italy"}, {"JP", "Japan"}, {"KR", "Korea (South)"},
	{"LT", "Lithuania"}, {"LU", "Luxembourg"}, {"LV", "Latvia"}, {"MX", "Mexico"},
	{"NL", "Netherlands"}, {"NO", "Norway"}, {"NZ", "New Zealand"}, {"PL", "Poland"},
	{"PT", "Portugal"}, {"RO", "Romania"}, {"SE", "Sweden"}, {"SG", "Singapore"},
	{"SI", "Slovenia"}, {"SK", "Slovakia"}, {"TR", "Turkey"}, {"TW", "Taiwan"},
	{"UA", "Ukraine"}, {"US", "United States"}, {"ZA", "South Africa"},
}

// Countries returns all countries sorted by name. It reads the tz
// database's iso3166.tab (present on Raspberry Pi OS) and falls back to a
// built-in list.
func Countries() []Country {
	data, err := os.ReadFile("/usr/share/zoneinfo/iso3166.tab")
	if err != nil {
		return fallbackCountries
	}
	var out []Country
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		code, name, ok := strings.Cut(line, "\t")
		if ok && ValidCountry(code) {
			out = append(out, Country{Code: code, Name: strings.TrimSpace(name)})
		}
	}
	if len(out) == 0 {
		return fallbackCountries
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
