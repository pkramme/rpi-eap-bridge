package nm

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// KeyFile builds a NetworkManager keyfile (GLib key file syntax) while
// keeping the order of sections and keys stable.
type KeyFile struct {
	comments []string
	sections []*Section
}

// Section is one [section] of a key file.
type Section struct {
	name string
	keys []string
	vals []string
}

// NewKeyFile returns an empty key file with optional header comment lines.
func NewKeyFile(comments ...string) *KeyFile {
	return &KeyFile{comments: comments}
}

// Section returns the named section, creating it if necessary.
func (k *KeyFile) Section(name string) *Section {
	for _, s := range k.sections {
		if s.name == name {
			return s
		}
	}
	s := &Section{name: name}
	k.sections = append(k.sections, s)
	return s
}

// SetString stores an escaped string value.
func (s *Section) SetString(key, value string) { s.SetRaw(key, EscapeString(value)) }

// SetBool stores a boolean value.
func (s *Section) SetBool(key string, v bool) { s.SetRaw(key, strconv.FormatBool(v)) }

// SetInt stores an integer value.
func (s *Section) SetInt(key string, v int) { s.SetRaw(key, strconv.Itoa(v)) }

// SetRaw stores a value that is already in key file syntax.
func (s *Section) SetRaw(key, raw string) {
	for i, k := range s.keys {
		if k == key {
			s.vals[i] = raw
			return
		}
	}
	s.keys = append(s.keys, key)
	s.vals = append(s.vals, raw)
}

// Bytes renders the key file.
func (k *KeyFile) Bytes() []byte {
	var b bytes.Buffer
	for _, c := range k.comments {
		b.WriteString("# " + c + "\n")
	}
	for i, s := range k.sections {
		if i > 0 || len(k.comments) > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("[" + s.name + "]\n")
		for j, key := range s.keys {
			b.WriteString(key + "=" + s.vals[j] + "\n")
		}
	}
	return b.Bytes()
}

// EscapeString escapes a value the way GLib's key file parser expects:
// backslash, newline, carriage return and tab are always escaped; spaces
// are escaped at the start and end of the value so they are not lost.
func EscapeString(s string) string {
	trailFrom := len(strings.TrimRight(s, " "))
	var b strings.Builder
	lead := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case ' ':
			if lead || i >= trailFrom {
				b.WriteString(`\s`)
			} else {
				b.WriteByte(' ')
			}
		default:
			b.WriteByte(c)
		}
		if c != ' ' {
			lead = false
		}
	}
	return b.String()
}

// UnescapeString reverses EscapeString (and also accepts "\;" as used in lists).
func UnescapeString(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(s) {
			return "", fmt.Errorf("value ends with a backslash")
		}
		i++
		switch s[i] {
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '\\':
			b.WriteByte('\\')
		case ';':
			b.WriteByte(';')
		default:
			return "", fmt.Errorf("invalid escape sequence \\%c", s[i])
		}
	}
	return b.String(), nil
}

// ByteList encodes bytes as a NetworkManager integer list, e.g. "101;100;117;".
// SSIDs are written this way so that any byte sequence is represented
// exactly and can never be confused with something else.
func ByteList(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		sb.WriteString(strconv.Itoa(int(c)))
		sb.WriteByte(';')
	}
	return sb.String()
}

// DecodeByteList parses an integer list written by ByteList.
func DecodeByteList(s string) ([]byte, bool) {
	s = strings.TrimSuffix(strings.TrimSpace(s), ";")
	if s == "" {
		return nil, false
	}
	var out []byte
	for _, part := range strings.Split(s, ";") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 || n > 255 {
			return nil, false
		}
		out = append(out, byte(n))
	}
	return out, true
}

// ParseKeyFile parses a key file into section -> key -> unescaped value. It
// understands the subset of the syntax that NetworkManager uses. It is used
// by tests and by the demo mode.
func ParseKeyFile(data []byte) (map[string]map[string]string, error) {
	res := map[string]map[string]string{}
	var cur map[string]string
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimLeft(strings.TrimRight(line, "\r"), " \t")
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: invalid section header", n+1)
			}
			name := line[1 : len(line)-1]
			if res[name] == nil {
				res[name] = map[string]string{}
			}
			cur = res[name]
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: key outside of a section", n+1)
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: missing '='", n+1)
		}
		uv, err := UnescapeString(strings.TrimLeft(v, " \t"))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
		cur[strings.TrimSpace(k)] = uv
	}
	return res, nil
}
