// Package tlsserve obtains and renews certificates in-process and builds the
// listener beacon serves TLS on.
//
// Certificates come from ACME over the DNS-01 challenge, which means the
// service never needs port 80 or an inbound connection to prove ownership —
// the right property behind an SNI-passthrough proxy. CertMagic keeps them
// renewed and swaps them into the running config without a restart, so there
// is no cron job and no second process.
package tlsserve

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
	proxyproto "github.com/pires/go-proxyproto"
	"golang.org/x/net/netutil"

	"beacon/internal/tlsfp"
)

// LetsEncryptStaging is worth using while testing: production has rate limits
// that are easy to hit when a config is not yet right.
const (
	CAProduction = certmagic.LetsEncryptProductionCA
	CAStaging    = certmagic.LetsEncryptStagingCA
)

type Options struct {
	Domains     []string
	Email       string
	CA          string
	CFAPIToken  string
	CFZoneToken string
	StorageDir  string
}

// Config prepares certificate management and returns a *tls.Config.
//
// ManageSync blocks until the certificate exists, so a misconfigured token or
// domain fails loudly at startup rather than on the first request.
func Config(ctx context.Context, opts Options) (*tls.Config, error) {
	if len(opts.Domains) == 0 {
		return nil, fmt.Errorf("no domains configured")
	}
	if opts.CFAPIToken == "" {
		return nil, fmt.Errorf("no Cloudflare API token configured")
	}

	certmagic.Default.Storage = &certmagic.FileStorage{
		Path: filepath.Join(opts.StorageDir, "certmagic"),
	}

	ca := opts.CA
	if ca == "" {
		ca = CAProduction
	}

	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = opts.Email
	certmagic.DefaultACME.CA = ca
	// DNS-01 only: no HTTP-01, no TLS-ALPN-01, so nothing needs to reach us.
	certmagic.DefaultACME.DisableHTTPChallenge = true
	certmagic.DefaultACME.DisableTLSALPNChallenge = true
	certmagic.DefaultACME.DNS01Solver = &certmagic.DNS01Solver{
		DNSManager: certmagic.DNSManager{
			DNSProvider: &cloudflare.Provider{
				APIToken:  opts.CFAPIToken,
				ZoneToken: opts.CFZoneToken,
			},
			PropagationTimeout: 5 * time.Minute,
		},
	}

	magic := certmagic.NewDefault()
	if err := magic.ManageSync(ctx, opts.Domains); err != nil {
		return nil, fmt.Errorf("obtain certificate for %v: %w", opts.Domains, err)
	}

	cfg := magic.TLSConfig()
	cfg.MinVersion = tls.VersionTLS12
	// magic.TLSConfig() returns NextProtos containing only the ACME TLS-ALPN
	// protocol. That challenge is disabled above, so replace the list outright
	// rather than prepending: leaving acme-tls/1 advertised would offer a
	// protocol this server will not actually speak.
	cfg.NextProtos = []string{"h2", "http/1.1"}
	// Record each ClientHello as it arrives. This is the only place it exists:
	// once the handshake completes the message is gone.
	return tlsfp.Capture(cfg), nil
}

// Listen opens the TLS listener, optionally reading a PROXY protocol header
// first so the original client address survives a TCP-level proxy.
//
// The policy is REQUIRE when enabled: beacon should be bound to loopback and
// reachable only through the proxy, so a connection without the header is
// unexpected and better refused than silently attributed to the proxy itself.
//
// Layering matters. PROXY protocol is plaintext at the very start of the
// stream, so it must be read before TLS; the fingerprint wrapper sits between
// that and TLS so the ClientHello handler has a connection to record against,
// while RemoteAddr still comes from the PROXY header.
// MaxConns bounds simultaneous connections per listener.
//
// Timeouts bound how long one connection lives, not how many exist. Each costs
// a TLS session, the captured ClientHello and up to a megabyte of HTTP/2 frame
// buffer, all before a request arrives, so without a cap the memory ceiling is
// whatever an attacker cares to open. Well above any real load this will see.
const MaxConns = 512

func Listen(addr string, cfg *tls.Config, proxyProtocol bool) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	// Innermost, under everything else. netutil wraps each accepted connection
	// in an unexported type, and tlsfp.ConnFromNet reaches the fingerprint by
	// walking *tls.Conn -> *tlsfp.Conn — so a limiter on the OUTSIDE leaves the
	// server holding a type it cannot see through, and every TLS and HTTP/2
	// fingerprint silently becomes null. Down here the chain the server sees is
	// unchanged and the limit still holds, because closing any wrapper closes
	// through to this one.
	ln = netutil.LimitListener(ln, MaxConns)

	if proxyProtocol {
		ln = &proxyproto.Listener{
			Listener: ln,
			ConnPolicy: func(proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
				return proxyproto.REQUIRE, nil
			},
			ReadHeaderTimeout: 10 * time.Second,
		}
	}

	return tls.NewListener(tlsfp.NewListener(ln), cfg), nil
}
