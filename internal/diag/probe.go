// Package diag contains the connection test and the log analysis that
// turns NetworkManager / wpa_supplicant messages into helpful hints.
package diag

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Item statuses.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
	Skip = "skip"
	Info = "info"
)

// Item is one line of a connection test.
type Item struct {
	Name   string
	Status string
	Detail string
	Hint   string
}

const (
	checkHost   = "connectivity-check.ubuntu.com"
	checkURL    = "http://connectivity-check.ubuntu.com/" // answers 204 No Content
	fallbackURL = "http://nmcheck.gnome.org/check_network_status.txt"
	fallbackOK  = "NetworkManager is online"
)

var tcpTargets = []struct{ addr, name string }{
	{"1.1.1.1:443", "1.1.1.1 (Cloudflare)"},
	{"9.9.9.9:443", "9.9.9.9 (Quad9)"},
	{"8.8.8.8:443", "8.8.8.8 (Google)"},
}

// Probe tests the internet connection from the Raspberry Pi in three steps:
// raw IP connectivity, name resolution (DNS) and loading a web page.
func Probe(ctx context.Context) []Item {
	return []Item{probeTCP(ctx), probeDNS(ctx), probeHTTP(ctx)}
}

func probeTCP(ctx context.Context) Item {
	const name = "Internet reachable"
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	type result struct {
		name string
		took time.Duration
		err  error
	}
	ch := make(chan result, len(tcpTargets))
	for _, t := range tcpTargets {
		go func(addr, label string) {
			start := time.Now()
			var d net.Dialer
			c, err := d.DialContext(ctx, "tcp4", addr)
			if err == nil {
				c.Close()
			}
			ch <- result{label, time.Since(start), err}
		}(t.addr, t.name)
	}
	var lastErr error
	for range tcpTargets {
		r := <-ch
		if r.err == nil {
			return Item{Name: name, Status: OK, Detail: fmt.Sprintf("Reached %s in %d ms.", r.name, r.took.Milliseconds())}
		}
		lastErr = r.err
	}
	return Item{
		Name:   name,
		Status: Fail,
		Detail: "No server on the internet could be reached (" + shortErr(lastErr) + ").",
		Hint:   "The WiFi is connected, but no traffic gets through. The network may block unknown devices, need a separate web login, or have a temporary problem.",
	}
}

func probeDNS(ctx context.Context) Item {
	const name = "Name lookup (DNS)"
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, checkHost)
	if err != nil || len(addrs) == 0 {
		if err == nil {
			err = errors.New("no addresses")
		}
		return Item{
			Name:   name,
			Status: Fail,
			Detail: "Looking up " + checkHost + " failed (" + shortErr(err) + ").",
			Hint:   "The network's DNS server does not answer. Websites will not load by name. Try again in a few minutes; if it persists, contact your IT department.",
		}
	}
	return Item{Name: name, Status: OK, Detail: checkHost + " → " + addrs[0]}
}

func probeHTTP(ctx context.Context) Item {
	const name = "Test website"
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	}
	status, _, loc, err := fetch(ctx, client, checkURL)
	if err == nil {
		switch {
		case status == http.StatusNoContent:
			return Item{Name: name, Status: OK, Detail: "A test page on the internet loaded correctly."}
		case status >= 300 && status < 400:
			return portalItem(name, "The test page was redirected to "+hostOf(loc)+".")
		default:
			return portalItem(name, fmt.Sprintf("The test page returned an unexpected answer (HTTP %d).", status))
		}
	}
	status, body, _, err2 := fetch(ctx, client, fallbackURL)
	if err2 == nil && status == http.StatusOK && strings.Contains(body, fallbackOK) {
		return Item{Name: name, Status: OK, Detail: "A test page on the internet loaded correctly."}
	}
	return Item{
		Name:   name,
		Status: Fail,
		Detail: "Test pages could not be loaded (" + shortErr(err) + ").",
		Hint:   "Websites do not load. If the steps above are green, try again in a minute.",
	}
}

func portalItem(name, detail string) Item {
	return Item{
		Name:   name,
		Status: Warn,
		Detail: detail,
		Hint:   "The network seems to want an additional login on a web page (a “captive portal”). Open a browser on a device that is connected to the bridge, visit http://neverssl.com and log in on the page that appears.",
	}
}

func fetch(ctx context.Context, c *http.Client, u string) (status int, body, location string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("User-Agent", "eap-bridge-connectivity-check")
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(b), resp.Header.Get("Location"), nil
}

func hostOf(loc string) string {
	if u, err := url.Parse(loc); err == nil && u.Host != "" {
		return u.Host
	}
	if loc == "" {
		return "another page"
	}
	return loc
}

func shortErr(err error) string {
	if err == nil {
		return "unknown error"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timed out"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return "name not found"
		}
		return "DNS error"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "connection refused"
	case strings.Contains(s, "network is unreachable"):
		return "network unreachable"
	case strings.Contains(s, "no route to host"):
		return "no route to host"
	}
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}
