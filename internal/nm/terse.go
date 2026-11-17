package nm

import (
	"strconv"
	"strings"
)

// SplitTerse splits one line of `nmcli -t` output into its fields. nmcli
// separates fields with ':' and escapes literal ':' and '\' with a backslash.
func SplitTerse(line string) []string {
	fields := make([]string, 0, 8)
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == ':':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(fields, cur.String())
}

// ParseFields parses multi-line terse output such as
// `nmcli -t -f GENERAL,IP4 device show wlan0`, where each line has the form
// KEY:VALUE. Indexed keys like IP4.ADDRESS[1] are collected under IP4.ADDRESS.
func ParseFields(out string) map[string][]string {
	m := make(map[string][]string)
	for _, line := range splitLines(out) {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if i := strings.IndexByte(key, '['); i > 0 {
			key = key[:i]
		}
		m[key] = append(m[key], unescapeTerse(val))
	}
	return m
}

// ParseState parses values like "100 (connected)" or
// "70 (connecting (getting IP configuration))".
func ParseState(s string) (int, string) {
	s = strings.TrimSpace(s)
	num, text, _ := strings.Cut(s, " ")
	n, err := strconv.Atoi(num)
	if err != nil {
		return StateUnknown, s
	}
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "(")
	text = strings.TrimSuffix(text, ")")
	return n, text
}

func splitLines(out string) []string {
	var res []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, "\r")
		if l != "" {
			res = append(res, l)
		}
	}
	return res
}

func unescapeTerse(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// blank maps nmcli's placeholder for empty values to "".
func blank(s string) string {
	if s == "--" {
		return ""
	}
	return s
}
