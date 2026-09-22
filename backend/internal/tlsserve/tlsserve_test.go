package tlsserve

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/pires/go-proxyproto"
)

func listener(t *testing.T, proxyProtocol bool) net.Listener {
	t.Helper()
	ln, err := Listen("127.0.0.1:0", &tls.Config{}, proxyProtocol)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// The policy is REQUIRE, not USE. That is the difference between "the peer
// address is whatever the edge told us" and "a connection that reaches this
// port without going through the edge is refused rather than reported as
// itself" — which is what stops the TLS port being a way to launder an
// address if it is ever exposed.
func TestProxyProtocolListenerRefusesAConnectionWithoutAHeader(t *testing.T) {
	ln := listener(t, true)

	accepted := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			accepted <- err
			return
		}
		defer conn.Close()
		// The PROXY header is read on the first read, not at Accept.
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = conn.Read(make([]byte, 1))
		accepted <- err
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	// A TLS ClientHello, with no PROXY header in front of it.
	if _, err := c.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x00, 0x00, 0x01, 0x00}); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case err := <-accepted:
		if err == nil {
			t.Fatal("a connection with no PROXY header was accepted and read from")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("neither accepted nor refused within 5s")
	}
}

// And the other half: with a header, the connection is accepted and the peer
// it reports is the one the header carried, not the machine that dialled.
func TestProxyProtocolListenerReportsTheForwardedPeer(t *testing.T) {
	ln := listener(t, true)

	type result struct {
		addr string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- result{err: err}
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		// RemoteAddr is only rewritten once the header has been read.
		_, _ = conn.Read(make([]byte, 1))
		got <- result{addr: conn.RemoteAddr().String()}
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	client := &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 51234}
	edge := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 443}
	header := proxyproto.HeaderProxyFromAddrs(2, client, edge)
	if _, err := header.WriteTo(c); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := c.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x01, 0x00}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	select {
	case r := <-got:
		if r.err != nil && !errors.Is(r.err, io.EOF) {
			t.Fatalf("accept: %v", r.err)
		}
		if want := "203.0.113.9:51234"; r.addr != want {
			t.Errorf("RemoteAddr = %q, want %q — the forwarded peer did not survive the "+
				"tlsfp and tls wrappers", r.addr, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no result within 5s")
	}
}

// Without the option the listener is ordinary: a plain connection is accepted.
func TestWithoutProxyProtocolAPlainConnectionIsAccepted(t *testing.T) {
	ln := listener(t, false)

	got := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- err
			return
		}
		conn.Close()
		got <- nil
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	select {
	case err := <-got:
		if err != nil {
			t.Errorf("Accept: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not accepted within 5s")
	}
}
