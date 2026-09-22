package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"beacon/internal/config"
	"beacon/internal/render"
)

func TestWantsPage(t *testing.T) {
	const browser = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/140.0 Safari/537.36"

	cases := []struct {
		name   string
		ua     string
		accept string
		query  string
		want   bool
	}{
		{"browser navigation", browser, "text/html,application/xhtml+xml", "", true},
		{"browser fetching json", browser, "application/json", "", false},
		{"browser but format override", browser, "text/html", "format=text", false},
		{"curl wildcard", "curl/8.7.1", "*/*", "", false},
		{"wget", "Wget/1.21.4", "*/*", "", false},
		{"httpie", "HTTPie/3.2.2", "*/*", "", false},
		{"no user agent", "", "text/html", "", false},
		{"html accept but script ua", "python-requests/2.32", "text/html", "", false},
		{"firefox", "Mozilla/5.0 Firefox/130.0", "text/html", "", true},
		{"empty accept", browser, "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := "/8.8.8.8"
			if tc.query != "" {
				target += "?" + tc.query
			}
			r := httptest.NewRequest("GET", target, nil)
			r.Header.Set("User-Agent", tc.ua)
			r.Header.Set("Accept", tc.accept)
			if got := wantsPage(r); got != tc.want {
				t.Errorf("wantsPage() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNegotiate(t *testing.T) {
	cases := []struct {
		name        string
		query       string
		accept      string
		wantJSON    bool
		wantVersion int
	}{
		{"default is text", "", "*/*", false, render.SchemaVersion},
		{"accept json", "", "application/json", true, render.SchemaVersion},
		{"format json beats accept", "format=json", "*/*", true, render.SchemaVersion},
		{"format text beats accept", "format=text", "application/json", false, render.SchemaVersion},
		{"legacy version", "v=1", "application/json", true, 1},
		{"unknown version falls back", "v=9", "application/json", true, render.SchemaVersion},
		{"format is case insensitive", "format=JSON", "*/*", true, render.SchemaVersion},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := "/8.8.8.8"
			if tc.query != "" {
				target += "?" + tc.query
			}
			r := httptest.NewRequest("GET", target, nil)
			r.Header.Set("Accept", tc.accept)
			gotJSON, gotVersion := negotiate(r)
			if gotJSON != tc.wantJSON || gotVersion != tc.wantVersion {
				t.Errorf("negotiate() = (%v, %d), want (%v, %d)",
					gotJSON, gotVersion, tc.wantJSON, tc.wantVersion)
			}
		})
	}
}

func TestIsFrontendPath(t *testing.T) {
	yes := []string{"/_next/static/chunk.js", "/_next/", "/favicon.ico", "/logo.png"}
	no := []string{"/", "/8.8.8.8", "/_nextfoo", "/logo.png.evil", "/favicon.ico/x"}

	for _, p := range yes {
		if !isFrontendPath(p) {
			t.Errorf("isFrontendPath(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if isFrontendPath(p) {
			t.Errorf("isFrontendPath(%q) = true, want false", p)
		}
	}
}

func TestCheckIP(t *testing.T) {
	cases := map[string]ipCheck{
		"8.8.8.8":         ipValid,
		"2606:4700::1111": ipValid,
		"::1":             ipValid,
		"999.1.1.1":       ipOctetOutOfRange,
		"256.0.0.1":       ipOctetOutOfRange,
		"notanip":         ipMalformed,
		"":                ipMalformed,
		"8.8.8":           ipMalformed,
	}
	for in, want := range cases {
		if got := checkIP(in); got != want {
			t.Errorf("checkIP(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name   string
		trust  bool
		realIP string
		xff    string
		remote string
		want   string
	}{
		{"untrusted ignores headers", false, "1.2.3.4", "5.6.7.8", "10.0.0.9:5555", "10.0.0.9"},
		// X-Forwarded-For is appended by proxies and so outranks X-Real-IP,
		// which a proxy may simply pass through from the client.
		{"xff outranks x-real-ip", true, "1.2.3.4", "5.6.7.8", "10.0.0.9:5555", "5.6.7.8"},
		{"x-real-ip used when no xff", true, "1.2.3.4", "", "10.0.0.9:5555", "1.2.3.4"},
		{"xff when no real-ip", true, "", "5.6.7.8", "10.0.0.9:5555", "5.6.7.8"},
		{"xff takes last hop", true, "", "9.9.9.9, 5.6.7.8", "10.0.0.9:5555", "5.6.7.8"},
		{"spoofed leading xff ignored", true, "", "evil, 5.6.7.8", "10.0.0.9:5555", "5.6.7.8"},
		{"garbage real-ip falls through", true, "not-an-ip", "5.6.7.8", "10.0.0.9:5555", "5.6.7.8"},
		{"garbage xff falls back to real-ip", true, "1.2.3.4", "nope", "10.0.0.9:5555", "1.2.3.4"},
		{"all garbage falls to peer", true, "nope", "also-nope", "10.0.0.9:5555", "10.0.0.9"},
		{"ipv6 xff", true, "", "2606:4700::1111", "10.0.0.9:5555", "2606:4700::1111"},
		{"xff with port", true, "", "5.6.7.8:1234", "10.0.0.9:5555", "5.6.7.8"},
		{"no headers", true, "", "", "10.0.0.9:5555", "10.0.0.9"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &server{cfg: config.Config{TrustProxyHeaders: tc.trust}}
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			if tc.realIP != "" {
				r.Header.Set("X-Real-IP", tc.realIP)
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := s.clientIP(r); got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// On a PROXY protocol listener the peer address is supplied out-of-band and is
// authoritative; headers inside the request come from the client itself.
func TestStripForwardedIgnoresClientHeaders(t *testing.T) {
	s := &server{cfg: config.Config{TrustProxyHeaders: true}}

	var got string
	h := stripForwarded(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = s.clientIP(r)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.7:443"
	r.Header.Set("X-Real-IP", "1.2.3.4")
	r.Header.Set("X-Forwarded-For", "5.6.7.8")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got != "203.0.113.7" {
		t.Errorf("clientIP = %q, want the PROXY peer 203.0.113.7 (headers must not win)", got)
	}
}

// Without the wrapper the same request is trusted, which is correct behind a
// real HTTP reverse proxy — the two paths must differ.
func TestWithoutStripForwardedHeadersAreTrusted(t *testing.T) {
	s := &server{cfg: config.Config{TrustProxyHeaders: true}}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.7:443"
	r.Header.Set("X-Real-IP", "1.2.3.4")
	if got := s.clientIP(r); got != "1.2.3.4" {
		t.Errorf("clientIP = %q, want 1.2.3.4", got)
	}
}

func TestCanonicalIP(t *testing.T) {
	cases := map[string]string{
		"::ffff:8.8.8.8":      "8.8.8.8", // v4-mapped echoes as v4, matching its family
		"8.8.8.8":             "8.8.8.8",
		"2606:4700::1111":     "2606:4700::1111",
		"2606:4700:0:0::1111": "2606:4700::1111",
		"notanip":             "notanip",
	}
	for in, want := range cases {
		if got := canonicalIP(in); got != want {
			t.Errorf("canonicalIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWantsPageOnlyHonouredFormatsSuppress(t *testing.T) {
	const browserUA = "Mozilla/5.0 (Macintosh) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36"

	suppress := []string{"json", "text", "txt", "plain", "JSON"}
	ignore := []string{"html", "xml", "", "garbage"}

	for _, f := range suppress {
		r := httptest.NewRequest("GET", "/8.8.8.8?format="+f, nil)
		r.Header.Set("User-Agent", browserUA)
		r.Header.Set("Sec-Fetch-Dest", "document")
		if wantsPage(r) {
			t.Errorf("format=%q did not suppress the page", f)
		}
	}
	for _, f := range ignore {
		r := httptest.NewRequest("GET", "/8.8.8.8?format="+f, nil)
		r.Header.Set("User-Agent", browserUA)
		r.Header.Set("Sec-Fetch-Dest", "document")
		if !wantsPage(r) {
			t.Errorf("unrecognised format=%q wrongly suppressed the page", f)
		}
	}
}

// Whether forwarded headers are believed decides whether a stranger can choose
// the address this service reports about them. It used to be decided inline in
// main, gated on PROXY protocol, and applied to only one of the two listeners —
// so the plain listener, which is the one compose publishes, trusted anything.
func TestTrustsForwardedHeaders(t *testing.T) {
	for _, tc := range []struct {
		name          string
		trust         bool
		proxyProtocol bool
		want          bool
		why           string
	}{
		{
			name: "default", want: false,
			why: "an untouched config must not believe a client's own headers",
		},
		{
			name: "opted in", trust: true, want: true,
			why: "an operator behind a proxy asked for this",
		},
		{
			name: "proxy protocol alone", proxyProtocol: true, want: false,
			why: "the peer is already authoritative",
		},
		{
			name: "proxy protocol wins over the opt-in", trust: true, proxyProtocol: true, want: false,
			why: "a forwarded header arriving with a PROXY header came from the client",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{TrustProxyHeaders: tc.trust, ProxyProtocol: tc.proxyProtocol}
			if got := trustsForwardedHeaders(cfg); got != tc.want {
				t.Errorf("trustsForwardedHeaders = %v, want %v: %s", got, tc.want, tc.why)
			}
		})
	}
}
