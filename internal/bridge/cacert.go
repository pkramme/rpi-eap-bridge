package bridge

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
)

// NormalizeCACert accepts PEM or DER encoded certificates (one or several)
// and returns them as PEM, together with a short description.
func NormalizeCACert(data []byte) ([]byte, string, error) {
	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, "", errors.New("the file contains a damaged certificate")
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		cs, err := x509.ParseCertificates(data)
		if err != nil || len(cs) == 0 {
			return nil, "", errors.New("the file does not contain a certificate (expected a .pem, .crt, .cer or .der file)")
		}
		certs = cs
	}
	var buf bytes.Buffer
	var names []string
	for _, c := range certs {
		if err := pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}); err != nil {
			return nil, "", err
		}
		name := c.Subject.CommonName
		if name == "" {
			name = c.Subject.String()
		}
		names = append(names, name)
	}
	desc := strings.Join(names, ", ")
	if len(desc) > 120 {
		desc = desc[:120] + "…"
	}
	return buf.Bytes(), desc, nil
}
