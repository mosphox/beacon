package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/net/netutil"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"beacon/internal/browser"
	"beacon/internal/config"
	"beacon/internal/geoip"
	"beacon/internal/h2fp"
	"beacon/internal/rdns"
	"beacon/internal/render"
	"beacon/internal/tlsfp"
	"beacon/internal/tlsserve"
)

var ipv4ShapeRe = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)

// The Next.js app owns these paths regardless of who is asking: they are its
// own assets, not lookups. Exact paths are matched exactly, so that a lookup
// for a name merely starting with one of them is still a lookup.
var (
	frontendPrefixes = []string{"/_next/"}
	frontendExact    = []string{"/favicon.ico", "/logo.png"}
)

type ipCheck int

const (
	ipValid ipCheck = iota
	ipMalformed
	ipOctetOutOfRange
)

type server struct {
	cfg      config.Config
	geo      *geoip.Registry
	rdns     *rdns.Resolver
	frontend http.Handler
}

func main() {
	log.SetFlags(log.LstdFlags)

	// The runtime image carries no shell tools, so the binary probes itself for
	// the container healthcheck.
	healthcheck := flag.Bool("healthcheck", false, "probe the local listener and exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if *healthcheck {
		os.Exit(probeHealth(cfg.ListenAddr))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	registry, err := buildRegistry(cfg)
	if err != nil {
		log.Fatalf("geoip: %v", err)
	}
	defer registry.Close()
	log.Printf("GeoIP sources: %s (refresh every %dh)",
		strings.Join(registry.Sources(), ", "), cfg.UpdatePeriodHours)

	srv := &server{cfg: cfg, geo: registry}

	if cfg.RDNSEnabled {
		srv.rdns = rdns.New(cfg.RDNSTimeout, cfg.RDNSCacheTTL)
		log.Printf("reverse DNS enabled (timeout %s, cache TTL %s)", cfg.RDNSTimeout, cfg.RDNSCacheTTL)
	}
	if cfg.FrontendURL != "" {
		u, err := url.Parse(cfg.FrontendURL)
		if err != nil {
			log.Fatalf("frontend url: %v", err)
		}
		srv.frontend = newFrontendProxy(u)
		log.Printf("proxying browser navigations to %s", cfg.FrontendURL)
	} else {
		log.Println("no frontend configured; serving plain text to browsers")
	}
	switch {
	case cfg.ProxyProtocol:
		log.Println("PROXY protocol in use: forwarded headers are stripped on every listener")
	case cfg.TrustProxyHeaders:
		log.Println("TRUST_PROXY_HEADERS is set: forwarded headers are believed. " +
			"Only safe if every listener is unreachable except through your proxy")
	default:
		log.Println("proxy headers are stripped; using the connection peer address")
	}

	// The refresh loop is waited on at shutdown so that a download in progress
	// cannot swap readers in after Close has already run.
	var refresh sync.WaitGroup
	refresh.Add(1)
	go func() {
		defer refresh.Done()
		registry.RefreshLoop(ctx)
	}()

	handler := srv.routes()

	// Decided once, for every listener. This used to live inline here, gated on
	// PROXY protocol and applied only to the TLS server, which left the plain
	// listener — the one compose publishes — believing whatever a client sent.
	// It is a named function so the decision itself can be tested; the bug was
	// never in stripForwarded, it was in when it got used.
	if !trustsForwardedHeaders(cfg) {
		handler = stripForwarded(handler)
	}

	var wg sync.WaitGroup
	var servers []*http.Server
	serveErr := make(chan error, 2)

	if cfg.TLSEnabled {
		tlsCfg, err := tlsserve.Config(ctx, tlsserve.Options{
			Domains:     cfg.Domains,
			Email:       cfg.ACMEEmail,
			CA:          acmeCA(cfg.ACMECA),
			CFAPIToken:  cfg.CFAPIToken,
			CFZoneToken: cfg.CFZoneToken,
			StorageDir:  cfg.DataDir,
		})
		if err != nil {
			log.Fatalf("tls: %v", err)
		}
		ln, err := tlsserve.Listen(cfg.TLSListenAddr, tlsCfg, cfg.ProxyProtocol)
		if err != nil {
			log.Fatalf("tls: %v", err)
		}
		tlsSrv := newHTTPServer("", handler)
		// Replace the standard HTTP/2 wiring with one that reads the client's
		// opening frames. Every h2 limit and timeout stays as the library set it.
		if err := h2fp.Configure(tlsSrv); err != nil {
			log.Fatalf("http2: %v", err)
		}
		log.Printf("serving HTTPS on %s for %s (PROXY protocol: %v)",
			cfg.TLSListenAddr, strings.Join(cfg.Domains, ", "), cfg.ProxyProtocol)

		servers = append(servers, serve(&wg, tlsSrv, ln, serveErr))
	}

	if cfg.ListenAddr != "" {
		ln, err := net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			log.Fatalf("listen on %s: %v", cfg.ListenAddr, err)
		}
		log.Printf("serving HTTP on %s", cfg.ListenAddr)
		servers = append(servers, serve(&wg, newHTTPServer("", handler), ln, serveErr))
	}

	if len(servers) == 0 {
		log.Fatal("nothing to listen on: set LISTEN_ADDR, TLS_ENABLED, or both")
	}

	select {
	case <-ctx.Done():
		log.Println("shutting down...")
	case err := <-serveErr:
		log.Printf("listener failed: %v", err)
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		if err := s.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
	}
	wg.Wait()
	refresh.Wait()
}

func buildRegistry(cfg config.Config) (*geoip.Registry, error) {
	client := geoip.DefaultClient()
	var providers []geoip.Provider

	// MaxMind first when present: it is the richer dataset, so it supplies the
	// top-level JSON fields.
	if cfg.MaxMindEnabled() {
		providers = append(providers,
			geoip.NewMaxMind(cfg.MaxMindAccountID, cfg.MaxMindLicenseKey, cfg.DataDir, client))
	}
	if cfg.DBIPEnabled {
		providers = append(providers, geoip.NewDBIP(cfg.DataDir, client))
	}

	return geoip.NewRegistry(cfg.DataDir, time.Duration(cfg.UpdatePeriodHours)*time.Hour, providers...)
}

func acmeCA(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "production", "prod":
		return tlsserve.CAProduction
	case "staging", "test":
		return tlsserve.CAStaging
	default:
		return v
	}
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: h,
		// Park the connection on the request context so a handler can reach
		// the ClientHello recorded during its handshake. The fingerprint is
		// not available yet here — the handshake has not run — so the
		// connection itself is what gets stored.
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if fc := tlsfp.ConnFromNet(c); fc != nil {
				return tlsfp.NewContext(ctx, fc)
			}
			return ctx
		},
		// net/http's default is 1 MiB, which is far more header than this
		// service has any use for and was the budget behind a User-Agent that
		// cost seconds of CPU to match against.
		MaxHeaderBytes:    32 << 10,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// serve runs one listener. A failure is reported rather than fatal: log.Fatal
// here would skip every defer and take the other, healthy listener down with
// it. The caller shuts everything down in order instead.
// maxConns bounds simultaneous connections per listener.
//
// Timeouts alone bound how long one connection lives, not how many exist. Each
// costs a TLS session, the captured fingerprint and up to a megabyte of HTTP/2
// frame buffer, all of it before a request arrives, so without a cap the
// memory ceiling is whatever an attacker cares to open. Well above any real
// load this service will see — it is a wall, not a throttle.
const maxConns = 512

func serve(wg *sync.WaitGroup, s *http.Server, ln net.Listener, fail chan<- error) *http.Server {
	ln = netutil.LimitListener(ln, maxConns)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case fail <- err:
			default:
			}
		}
	}()
	return s
}

// probeHealth is the other half of the container healthcheck: it asks the
// running instance for /healthz and turns the answer into an exit status.
func probeHealth(listenAddr string) int {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil || port == "" {
		fmt.Fprintf(os.Stderr, "healthcheck: cannot derive a port from %q\n", listenAddr)
		return 1
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

// secureHeaders wraps the whole mux, including the frontend proxy, so these
// reach the page as well as the API.
//
// The CSP is deliberately tight because it can be: next/font self-hosts the
// two families at build time, so there is no font CDN to allow, and the page
// only ever fetches its own path. 'unsafe-inline' for scripts is Next's inline
// bootstrap, which cannot be removed without wiring per-request nonces.
//
// HSTS is only sent over TLS. Sending it from the plain listener would pin a
// browser to HTTPS for a host that may not serve it.
func secureHeaders(next http.Handler) http.Handler {
	// AGPL-3.0 section 13 obliges a network service to offer its source to the
	// users interacting with it. The page carries this in its footer; a client
	// that only ever sees JSON or plain text needs it somewhere too, and the
	// plain-text format is a fixed one line that cannot take it.
	//
	// A fork that modifies beacon and runs it must point this at their own
	// source. That is the whole substance of the clause.
	const sourceURL = "https://github.com/mosphox/beacon"

	const csp = "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"font-src 'self'; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'none'; " +
		"form-action 'none'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", csp)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Source-Code", sourceURL)
		h.Set("Link", `<https://www.gnu.org/licenses/agpl-3.0.html>; rel="license"`)
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	// Registered explicitly so it beats the /{ip...} wildcard. The service only
	// listens once its databases are open, so reaching this at all means ready.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"sources": s.geo.Sources(),
		})
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		s.serveLookup(w, r, s.clientIP(r))
	})
	mux.HandleFunc("GET /{ip...}", func(w http.ResponseWriter, r *http.Request) {
		if s.frontend != nil && isFrontendPath(r.URL.Path) {
			s.frontend.ServeHTTP(w, r)
			return
		}
		ipPath := strings.TrimSuffix(r.PathValue("ip"), "/")
		if c := checkIP(ipPath); c != ipValid {
			// A browser asking for /some/page should still get the app, which
			// renders its own error, rather than a bare JSON body.
			if s.frontend != nil && wantsPage(r) {
				s.frontend.ServeHTTP(w, r)
				return
			}
			status, detail := ipCheckError(c)
			writeError(w, status, detail)
			return
		}
		s.serveLookup(w, r, canonicalIP(ipPath))
	})
	return secureHeaders(mux)
}

func (s *server) serveLookup(w http.ResponseWriter, r *http.Request, ip string) {
	if s.frontend != nil && wantsPage(r) {
		s.frontend.ServeHTTP(w, r)
		return
	}

	asJSON, version := negotiate(r)

	// Only the v2 JSON body carries a hostname, and the lookup can cost a DNS
	// round trip, so don't make plain-text callers pay for it.
	hostname := ""
	if s.rdns != nil && asJSON && version != 1 {
		hostname = s.rdns.Lookup(r.Context(), ip)
	}

	resp := render.New(ip, hostname, s.geo.LookupAll(ip))
	if asJSON && version != 1 {
		resp = resp.
			WithTLS(tlsfp.FromContext(r.Context()), negotiatedTLS(r)).
			WithHTTP2(h2fp.FromContext(r.Context()))
	}
	resp.Write(w, asJSON, version)
}

// negotiatedTLS reports what the handshake settled on, as opposed to what the
// client offered. Nil for a plain HTTP request.
func negotiatedTLS(r *http.Request) *render.NegotiatedTLS {
	cs := r.TLS
	if cs == nil {
		return nil
	}
	n := &render.NegotiatedTLS{
		Version:     tlsVersionName(cs.Version),
		CipherSuite: tls.CipherSuiteName(cs.CipherSuite),
		ALPN:        cs.NegotiatedProtocol,
		ServerName:  cs.ServerName,
		Resumed:     cs.DidResume,
		ECHAccepted: cs.ECHAccepted,
	}
	if cs.CurveID != 0 {
		n.CurveID = curveName(cs.CurveID)
	}
	return n
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}

// curveName covers the groups Go will actually negotiate; anything else is
// reported by number rather than guessed at.
func curveName(id tls.CurveID) string {
	switch id {
	case tls.CurveP256:
		return "secp256r1"
	case tls.CurveP384:
		return "secp384r1"
	case tls.CurveP521:
		return "secp521r1"
	case tls.X25519:
		return "x25519"
	case tls.X25519MLKEM768:
		return "X25519MLKEM768"
	default:
		return fmt.Sprintf("0x%04x", uint16(id))
	}
}

// negotiate decides between JSON and plain text, and which schema version.
// An explicit ?format= wins over Accept, and ?v= selects the schema.
func negotiate(r *http.Request) (asJSON bool, version int) {
	version = render.SchemaVersion
	if r.URL.Query().Get("v") == "1" {
		version = 1
	}

	switch strings.ToLower(r.URL.Query().Get("format")) {
	case "json":
		return true, version
	case "text", "txt", "plain":
		return false, version
	}

	return browser.WantsJSON(r.Header), version
}

// wantsPage reports whether this request should be handed to the web app.
// Only a format we actually honour suppresses the page; an unrecognised one is
// ignored rather than silently downgrading a browser to plain text.
func wantsPage(r *http.Request) bool {
	switch strings.ToLower(r.URL.Query().Get("format")) {
	case "json", "text", "txt", "plain":
		return false
	}
	return browser.IsNavigation(r.Header)
}

func isFrontendPath(p string) bool {
	for _, prefix := range frontendPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	for _, exact := range frontendExact {
		if p == exact {
			return true
		}
	}
	return false
}

func newFrontendProxy(target *url.URL) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(target)
	inner := proxy.Director
	proxy.Director = func(r *http.Request) {
		// Drop whatever the client sent; the upstream should only ever see a
		// chain we vouch for.
		r.Header.Del("X-Real-IP")
		r.Header.Del("X-Forwarded-For")
		inner(r)
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("frontend proxy: %v", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("frontend unavailable\n"))
	}
	return proxy
}

// canonicalIP renders an address in its usual form, so that a request for
// ::ffff:8.8.8.8 echoes 8.8.8.8 and agrees with the family reported for it.
func canonicalIP(p string) string {
	if ip := net.ParseIP(p); ip != nil {
		return ip.String()
	}
	return p
}

func checkIP(p string) ipCheck {
	if net.ParseIP(p) != nil {
		return ipValid
	}
	if ipv4ShapeRe.MatchString(p) {
		return ipOctetOutOfRange
	}
	return ipMalformed
}

func ipCheckError(c ipCheck) (int, string) {
	switch c {
	case ipMalformed:
		return http.StatusNotFound, "Invalid IP address format"
	case ipOctetOutOfRange:
		return http.StatusBadRequest, "Invalid IP address"
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

// stripForwarded removes forwarded headers before the handler sees them.
//
// It wraps the PROXY protocol listener, where RemoteAddr is already the true
// client address supplied out-of-band at the TCP layer. The HTTP headers in
// that request come from the client itself, inside its own TLS session, so
// they are attacker-controlled and must not be allowed to override it.
// trustsForwardedHeaders reports whether this deployment should believe a
// request's own X-Forwarded-For / X-Real-IP.
//
// Only when an operator has said so, and never alongside PROXY protocol: that
// carries the true peer in the connection itself, so a forwarded header
// arriving with it can only have been set by the client.
func trustsForwardedHeaders(cfg config.Config) bool {
	return cfg.TrustProxyHeaders && !cfg.ProxyProtocol
}

func stripForwarded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("X-Real-IP")
		r.Header.Del("X-Forwarded-For")
		next.ServeHTTP(w, r)
	})
}

// clientIP resolves the address to report for this request.
//
// X-Forwarded-For is consulted first, and only its LAST entry. Proxies append
// the peer they saw, so the last hop is what the nearest proxy actually
// observed; everything to its left may have been supplied by the caller.
//
// X-Real-IP is only a fallback, because it is a single value that proxies
// *set* rather than append — and a proxy that does not set it (Caddy's
// reverse_proxy does not) passes the client's own value straight through. A
// caller that sends both cannot therefore override the appended chain.
//
// Neither is worth anything unless beacon is only reachable through that
// proxy; see TRUST_PROXY_HEADERS. Values that do not parse are ignored rather
// than echoed back.
func (s *server) clientIP(r *http.Request) string {
	peer := peerAddr(r)
	if !s.cfg.TrustProxyHeaders {
		return peer
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		hops := strings.Split(xff, ",")
		if ip := parseIP(hops[len(hops)-1]); ip != "" {
			return ip
		}
	}
	if ip := parseIP(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	return peer
}

func peerAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// parseIP validates and canonicalises one header value, returning "" if it is
// not an address. A port is tolerated, since some proxies include one.
func parseIP(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if ip := net.ParseIP(v); ip != nil {
		return ip.String()
	}
	if host, _, err := net.SplitHostPort(v); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}
	return ""
}

func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}
