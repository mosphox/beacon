package tlsfp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"
)

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// The whole path: a real handshake over a wrapped listener, the ClientHello
// recorded against the connection, and the handler reaching it through the
// request context exactly as the service does.
func TestCaptureOverRealHandshake(t *testing.T) {
	cfg := Capture(&tls.Config{
		Certificates: []tls.Certificate{selfSigned(t)},
		NextProtos:   []string{"h2", "http/1.1"},
	})

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := tls.NewListener(NewListener(raw), cfg)
	defer ln.Close()

	got := make(chan *Fingerprint, 1)
	srv := &http.Server{
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if fc := ConnFromNet(c); fc != nil {
				return NewContext(ctx, fc)
			}
			return ctx
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got <- FromContext(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go srv.Serve(ln)
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"},
	}}
	resp, err := client.Get("https://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	select {
	case fp := <-got:
		if fp == nil {
			t.Fatal("handler saw no fingerprint for a TLS request")
		}
		if fp.JA3Hash == "" || len(fp.JA3Hash) != 32 {
			t.Errorf("ja3_hash = %q, want 32 hex characters", fp.JA3Hash)
		}
		if len(fp.JA4) < 10 || fp.JA4[0] != 't' {
			t.Errorf("ja4 = %q, want a t-prefixed fingerprint", fp.JA4)
		}
		if len(fp.CipherSuites) == 0 || len(fp.Extensions) == 0 {
			t.Error("client hello came back empty")
		}
		if fp.ServerName != "localhost" {
			t.Errorf("server_name = %q, want localhost", fp.ServerName)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never ran")
	}
}

// A plain HTTP request must not carry a fingerprint, and must not panic
// looking for one.
func TestNoFingerprintWithoutTLS(t *testing.T) {
	if fp := FromContext(context.Background()); fp != nil {
		t.Errorf("FromContext on a bare context = %v, want nil", fp)
	}
	if c := ConnFromNet(nil); c != nil {
		t.Errorf("ConnFromNet(nil) = %v, want nil", c)
	}
	var nilConn *Conn
	if fp := nilConn.Fingerprint(); fp != nil {
		t.Errorf("(*Conn)(nil).Fingerprint() = %v, want nil", fp)
	}
}
