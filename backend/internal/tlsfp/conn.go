package tlsfp

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
)

// Conn carries the fingerprint of the handshake that happened on it.
//
// The ClientHello arrives during the handshake, long before a request is
// routed, and http.Server hands the handler a *tls.Conn rather than whatever
// the listener produced. So the fingerprint is parked on the connection and
// looked up later, through tls.Conn.NetConn.
type Conn struct {
	net.Conn

	mu sync.Mutex
	fp *Fingerprint
}

func (c *Conn) set(fp *Fingerprint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fp = fp
}

// Fingerprint returns what was captured, or nil if the handshake has not
// happened or the client hello could not be read.
func (c *Conn) Fingerprint() *Fingerprint {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fp
}

// Listener tags each accepted connection so the handshake has somewhere to
// record its fingerprint. Wrap the raw listener with this *before* handing it
// to tls.NewListener.
type Listener struct {
	net.Listener
}

func NewListener(inner net.Listener) *Listener { return &Listener{Listener: inner} }

func (l *Listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &Conn{Conn: c}, nil
}

// Capture returns cfg with a hook that records each ClientHello. Any existing
// GetConfigForClient is preserved and still decides the handshake; this only
// observes.
func Capture(cfg *tls.Config) *tls.Config {
	inner := cfg.GetConfigForClient
	cfg.GetConfigForClient = func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
		if c, ok := chi.Conn.(*Conn); ok {
			c.set(New(chi))
		}
		if inner != nil {
			return inner(chi)
		}
		return nil, nil
	}
	return cfg
}

type contextKey struct{}

// NewContext stores the connection so a handler can reach its fingerprint.
func NewContext(ctx context.Context, c *Conn) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
}

// FromContext returns the fingerprint for this request's connection, or nil
// when the request did not arrive over a fingerprinted TLS listener.
func FromContext(ctx context.Context) *Fingerprint {
	c, _ := ctx.Value(contextKey{}).(*Conn)
	return c.Fingerprint()
}

// ConnFromNet unwraps whatever http.Server hands to ConnContext.
func ConnFromNet(c net.Conn) *Conn {
	switch v := c.(type) {
	case *Conn:
		return v
	case *tls.Conn:
		inner, _ := v.NetConn().(*Conn)
		return inner
	}
	return nil
}
