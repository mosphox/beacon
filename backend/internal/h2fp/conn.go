package h2fp

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"

	"golang.org/x/net/http2"
)

// Conn observes an HTTP/2 stream on its way to the real server.
//
// Reads pass through untouched; a copy is fed to the parser until the first
// request has been seen, after which the wrapper costs one comparison per read.
type Conn struct {
	net.Conn

	mu sync.Mutex
	p  parser
}

func newConn(c net.Conn) *Conn { return &Conn{Conn: c} }

func (c *Conn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.mu.Lock()
		if !c.p.done && !c.p.give {
			c.p.feed(b[:n])
		}
		c.mu.Unlock()
	}
	return n, err
}

// Fingerprint returns the completed fingerprint, or nil if the client's first
// request has not been parsed.
func (c *Conn) Fingerprint() *Fingerprint {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.p.done {
		return nil
	}
	fp := c.p.fp
	return &fp
}

// ConnectionState forwards the TLS state of the connection underneath.
//
// http2.ServeConn discovers TLS through this method, so without it a wrapped
// connection would look like cleartext and requests would arrive with no
// r.TLS.
func (c *Conn) ConnectionState() tls.ConnectionState {
	if cs, ok := c.Conn.(interface {
		ConnectionState() tls.ConnectionState
	}); ok {
		return cs.ConnectionState()
	}
	return tls.ConnectionState{}
}

type contextKey struct{}

func newContext(ctx context.Context, c *Conn) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
}

// FromContext returns the fingerprint for this request's connection, or nil
// when the request did not arrive over HTTP/2.
func FromContext(ctx context.Context) *Fingerprint {
	c, _ := ctx.Value(contextKey{}).(*Conn)
	return c.Fingerprint()
}

// baseContexter is how net/http hands a connection's context to an ALPN
// handler; the TLSNextProto signature predates context support.
type baseContexter interface {
	BaseContext() context.Context
}

// Configure installs HTTP/2 on srv with fingerprinting in place of the
// standard handler.
//
// It configures h2 normally first, so every limit and timeout is whatever the
// library would have chosen, then replaces the ALPN entry with one that wraps
// the connection before handing it to the same http2.Server.
func Configure(srv *http.Server) error {
	h2 := &http2.Server{}
	if err := http2.ConfigureServer(srv, h2); err != nil {
		return err
	}

	srv.TLSNextProto["h2"] = func(s *http.Server, tc *tls.Conn, h http.Handler) {
		sniffed := newConn(tc)

		ctx := context.Background()
		if bc, ok := h.(baseContexter); ok && bc.BaseContext() != nil {
			ctx = bc.BaseContext()
		}

		h2.ServeConn(sniffed, &http2.ServeConnOpts{
			Context:    newContext(ctx, sniffed),
			BaseConfig: s,
			Handler:    h,
		})
	}
	return nil
}
