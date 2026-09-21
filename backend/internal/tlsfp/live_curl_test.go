package tlsfp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// TestAgainstRealCurl drives an actual curl through the capture path and
// compares the result with what BrowserLeaks computed for the same curl
// release. It is the end-to-end counterpart to the synthetic golden test.
//
// curl's ClientHello changes between releases, so the comparison only holds
// for the version these values were recorded from; other versions still
// exercise the plumbing and are checked for shape instead.
func TestAgainstRealCurl(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl not available")
	}

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

	srv := &http.Server{
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if fc := ConnFromNet(c); fc != nil {
				return NewContext(ctx, fc)
			}
			return ctx
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(FromContext(r.Context()))
		}),
	}
	go srv.Serve(ln)
	defer srv.Close()

	// Reach it by name, not by address: an IP literal suppresses SNI, which
	// changes the JA4 prefix and the wire-order extension list.
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(curl, "-sk", "--max-time", "10",
		"https://localhost:"+port+"/").Output()
	if err != nil {
		t.Fatalf("curl: %v", err)
	}

	var fp Fingerprint
	if err := json.Unmarshal(out, &fp); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}

	if len(fp.JA3Hash) != 32 || !strings.HasPrefix(fp.JA4, "t1") {
		t.Fatalf("implausible fingerprint from a real client: ja3=%q ja4=%q", fp.JA3Hash, fp.JA4)
	}

	// Values recorded from tls.browserleaks.com for curl 8.7.1.
	const recordedFor = "curl 8.7.1"
	want := map[string]string{
		"ja3_hash":  "375c6162a492dfbf2795909110ce8424",
		"ja3n_hash": "90a369ebd76665d3296677774ee22ea2",
		"ja4":       "t13d4907h2_0d8feac7bc37_7395dae3b2f3",
		"ja4_o":     "t13d4907h2_2677ac475d6b_c6f9150fbe3b",
	}

	ver, _ := exec.Command(curl, "--version").Output()
	if !strings.HasPrefix(string(ver), recordedFor) {
		t.Logf("local curl is not %s; checked plumbing only, not the recorded values", recordedFor)
		return
	}

	got := map[string]string{
		"ja3_hash": fp.JA3Hash, "ja3n_hash": fp.JA3NHash,
		"ja4": fp.JA4, "ja4_o": fp.JA4O,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s for %s\n got %s\nwant %s (BrowserLeaks)", k, recordedFor, got[k], w)
		}
	}
}
