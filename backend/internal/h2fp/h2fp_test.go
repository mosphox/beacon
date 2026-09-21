package h2fp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// The Akamai string is a fixed rendering of the opening frames; this pins it
// against a fingerprint published by BrowserLeaks, including its MD5.
func TestAkamaiStringMatchesPublishedExample(t *testing.T) {
	fp := &Fingerprint{
		Settings: []Setting{
			{ID: 3, Value: 100},
			{ID: 4, Value: 10485760},
			{ID: 2, Value: 0},
		},
		WindowUpdate:      1048510465,
		PseudoHeaderOrder: []string{"m", "s", "a", "p"},
	}
	fp.build()

	const wantRaw = "3:100;4:10485760;2:0|1048510465|0|m,s,a,p"
	const wantHash = "64a832f547be33249bf4d33e8a46c5dc"

	if fp.Raw != wantRaw {
		t.Errorf("akamai\n got %s\nwant %s", fp.Raw, wantRaw)
	}
	if fp.Hash != wantHash {
		t.Errorf("akamai_hash\n got %s\nwant %s", fp.Hash, wantHash)
	}
}

// A setting whose value is zero must still appear: "2:0" is meaningful, and
// dropping it would collide with clients that never sent setting 2 at all.
func TestZeroValuedSettingIsKept(t *testing.T) {
	fp := &Fingerprint{
		Settings:          []Setting{{ID: 2, Value: 0}},
		PseudoHeaderOrder: []string{"m"},
	}
	fp.build()
	if !strings.HasPrefix(fp.Raw, "2:0|") {
		t.Errorf("akamai = %q, want the zero-valued setting kept", fp.Raw)
	}
}

// The two absent-field sentinels are deliberately different widths: an absent
// WINDOW_UPDATE is "00" and an absent PRIORITY is "0". The asymmetry is in the
// original format, and getting it wrong changes the hash.
func TestAbsentFieldsUseTheirOwnSentinels(t *testing.T) {
	fp := &Fingerprint{PseudoHeaderOrder: []string{"m", "s", "a", "p"}}
	fp.build()
	if fp.Raw != "|00|0|m,s,a,p" {
		t.Errorf("akamai = %q, want |00|0| for absent window update and priority", fp.Raw)
	}
}

// Only standalone PRIORITY frames belong in the fingerprint. A HEADERS frame
// carrying priority is parsed for framing but must not add an entry — Firefox
// sends exactly this combination.
func TestHeadersCarriedPriorityIsNotCounted(t *testing.T) {
	var p parser
	p.sawPreface = true

	// PRIORITY on stream 3, then HEADERS on stream 15 with the PRIORITY flag.
	priority := append([]byte{0, 0, 5, framePriority, 0, 0, 0, 0, 3},
		0x00, 0x00, 0x00, 0x00, 200)

	hdrBlock := []byte{0x82, 0x86, 0x84, 0x41, 0x0a} // indexed :method :scheme :path, then :authority
	hdrBlock = append(hdrBlock, []byte("localhost:")...)
	payload := append([]byte{0x00, 0x00, 0x00, 0x00, 100}, hdrBlock...)
	headers := append([]byte{
		byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload)),
		frameHeaders, flagEndHeaders | flagPriority, 0, 0, 0, 15,
	}, payload...)

	p.feed(append(priority, headers...))

	if len(p.fp.Priorities) != 1 {
		t.Fatalf("captured %d priority entries, want 1 (the standalone frame only): %+v",
			len(p.fp.Priorities), p.fp.Priorities)
	}
	if got := p.fp.Priorities[0]; got.StreamID != 3 || got.Weight != 201 {
		t.Errorf("priority = %+v, want stream 3 weight 201", got)
	}
}

// A client that never sets END_HEADERS must not be able to grow the buffered
// header block without limit.
func TestContinuationFloodIsBounded(t *testing.T) {
	var p parser
	p.sawPreface = true

	body := make([]byte, 16384)
	frame := func(typ, flags byte) []byte {
		return append([]byte{
			byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body)),
			typ, flags, 0, 0, 0, 1,
		}, body...)
	}

	p.feed(frame(frameHeaders, 0)) // no END_HEADERS
	for i := 0; i < 200 && !p.give; i++ {
		p.feed(frame(frameContinuation, 0))
	}

	if !p.give {
		t.Errorf("parser still accumulating after %d KiB of CONTINUATION", 200*16)
	}
	if len(p.headerBlk) > maxHeaderBlock {
		t.Errorf("header block grew to %d bytes, past the %d cap", len(p.headerBlk), maxHeaderBlock)
	}
}

func TestPriorityRendering(t *testing.T) {
	fp := &Fingerprint{
		Priorities: []Priority{
			{StreamID: 3, Exclusive: false, DependsOn: 0, Weight: 201},
			{StreamID: 5, Exclusive: true, DependsOn: 3, Weight: 101},
		},
		PseudoHeaderOrder: []string{"m"},
	}
	fp.build()
	if !strings.Contains(fp.Raw, "|3:0:0:201,5:1:3:101|") {
		t.Errorf("akamai = %q, want both priority frames rendered", fp.Raw)
	}
}

// The wire carries weight-1, so a frame holding 200 means a weight of 201.
func TestPriorityWeightIsOffByOneOnTheWire(t *testing.T) {
	p := priorityFrom(3, []byte{0x80, 0x00, 0x00, 0x05, 200})
	if !p.Exclusive || p.DependsOn != 5 || p.Weight != 201 {
		t.Errorf("priorityFrom = %+v, want exclusive dep=5 weight=201", p)
	}
}

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

// serveFingerprint starts a real HTTPS server with h2 fingerprinting and
// returns its address.
func serveFingerprint(t *testing.T) string {
	t.Helper()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{selfSigned(t)},
		NextProtos:   []string{"h2", "http/1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"proto": r.Proto,
				"tls":   r.TLS != nil,
				"fp":    FromContext(r.Context()),
			})
		}),
	}
	if err := Configure(srv); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	return ln.Addr().String()
}

type probe struct {
	Proto string       `json:"proto"`
	TLS   bool         `json:"tls"`
	FP    *Fingerprint `json:"fp"`
}

// The full path with a real HTTP/2 client: frames observed on the way past,
// the real server still serving normally, and the fingerprint reaching the
// handler.
func TestCaptureOverRealHTTP2(t *testing.T) {
	addr := serveFingerprint(t)

	client := &http.Client{Transport: &http2.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"},
	}}
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var got probe
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Proto != "HTTP/2.0" {
		t.Fatalf("proto = %q, want HTTP/2.0", got.Proto)
	}
	// The wrapper must not hide TLS from the server underneath it.
	if !got.TLS {
		t.Error("r.TLS was nil: the connection wrapper did not forward TLS state")
	}
	if got.FP == nil {
		t.Fatal("no HTTP/2 fingerprint reached the handler")
	}
	if len(got.FP.Settings) == 0 {
		t.Error("no SETTINGS captured")
	}
	if len(got.FP.Hash) != 32 {
		t.Errorf("akamai_hash = %q, want 32 hex characters", got.FP.Hash)
	}
	if strings.Join(got.FP.PseudoHeaderOrder, ",") == "" {
		t.Error("no pseudo-header order captured")
	}
	// Go's client emits its pseudo-headers alphabetically: :authority,
	// :method, :path, :scheme. curl uses the more common m,s,a,p — which is
	// exactly the sort of difference this fingerprint exists to expose.
	if want := "a,m,p,s"; strings.Join(got.FP.PseudoHeaderOrder, ",") != want {
		t.Errorf("pseudo_header_order = %v, want %s for Go's client",
			got.FP.PseudoHeaderOrder, want)
	}
	if !strings.Contains(got.FP.Raw, "|") {
		t.Errorf("akamai = %q, want the pipe-separated form", got.FP.Raw)
	}
	t.Logf("Go http2 client: %s", got.FP.Raw)
}

// The same connection must keep serving normally after the fingerprint is
// complete, and later requests must still see it.
func TestSubsequentRequestsStillWork(t *testing.T) {
	addr := serveFingerprint(t)

	client := &http.Client{Transport: &http2.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"},
	}}

	var first string
	for i := 0; i < 5; i++ {
		resp, err := client.Get("https://" + addr + "/")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		var got probe
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		resp.Body.Close()

		if got.FP == nil {
			t.Fatalf("request %d had no fingerprint", i)
		}
		if i == 0 {
			first = got.FP.Raw
			continue
		}
		if got.FP.Raw != first {
			t.Errorf("request %d fingerprint drifted:\n got %s\nwant %s", i, got.FP.Raw, first)
		}
	}
}

// HTTP/1.1 over the same server must work and carry no HTTP/2 fingerprint.
func TestHTTP11HasNoFingerprint(t *testing.T) {
	addr := serveFingerprint(t)

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "localhost",
			NextProtos:         []string{"http/1.1"},
		},
	}}
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var got probe
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Proto != "HTTP/1.1" {
		t.Fatalf("proto = %q, want HTTP/1.1", got.Proto)
	}
	if got.FP != nil {
		t.Errorf("HTTP/1.1 request carried an HTTP/2 fingerprint: %+v", got.FP)
	}
}

// curl negotiates h2 with its own settings, which must differ from Go's —
// that difference is the entire point of the fingerprint.
func TestRealCurlDiffersFromGoClient(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl not available")
	}
	addr := serveFingerprint(t)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(curl, "-sk", "--http2", "--max-time", "10",
		"https://localhost:"+port+"/").Output()
	if err != nil {
		t.Fatalf("curl: %v", err)
	}
	var viaCurl probe
	if err := json.Unmarshal(out, &viaCurl); err != nil {
		t.Fatalf("decode curl response: %v\n%s", err, out)
	}
	if viaCurl.Proto != "HTTP/2.0" {
		t.Skipf("curl did not negotiate h2 (got %s)", viaCurl.Proto)
	}
	if viaCurl.FP == nil {
		t.Fatal("no fingerprint for curl")
	}

	client := &http.Client{Transport: &http2.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"},
	}}
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	var viaGo probe
	_ = json.NewDecoder(resp.Body).Decode(&viaGo)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	t.Logf("curl: %s", viaCurl.FP.Raw)
	t.Logf("go:   %s", viaGo.FP.Raw)

	if viaCurl.FP.Hash == viaGo.FP.Hash {
		t.Errorf("curl and Go produced the same fingerprint %s; "+
			"two different HTTP/2 stacks should not collide", viaCurl.FP.Hash)
	}

	// Recorded from tls.browserleaks.com for curl 8.7.1. Matching an
	// independent implementation on a real client is the actual proof that
	// the frame parsing is right.
	const recordedFor = "curl 8.7.1"
	const recorded = "3:100;4:10485760;2:0|1048510465|0|m,s,a,p"

	ver, _ := exec.Command(curl, "--version").Output()
	if !strings.HasPrefix(string(ver), recordedFor) {
		t.Logf("local curl is not %s; compared clients only, not the recorded value", recordedFor)
		return
	}
	if viaCurl.FP.Raw != recorded {
		t.Errorf("akamai for %s\n got %s\nwant %s (BrowserLeaks)",
			recordedFor, viaCurl.FP.Raw, recorded)
	}
}

// settingsFrame builds a SETTINGS frame carrying n entries with unknown IDs.
// RFC 9113 requires unknown settings to be ignored, so a peer can send these
// indefinitely without the HTTP/2 server objecting.
func settingsFrame(n int) []byte {
	payload := make([]byte, 0, n*6)
	for i := 0; i < n; i++ {
		id := uint16(0xf000 + i)
		payload = append(payload, byte(id>>8), byte(id), 0, 0, 0, 0)
	}
	return append([]byte{
		byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload)),
		frameSettings, 0, 0, 0, 0, 0,
	}, payload...)
}

// A peer streaming SETTINGS frames forever must not grow the fingerprint
// without bound. maxSniff does not help here: it bounds the frame buffer, and
// that buffer drains as each frame completes.
func TestSettingsFloodIsBounded(t *testing.T) {
	var p parser
	p.sawPreface = true

	for i := 0; i < 5000 && !p.give; i++ {
		p.feed(settingsFrame(100))
	}

	if !p.give {
		t.Fatalf("parser still accumulating after 5000 SETTINGS frames (%d entries)",
			len(p.fp.Settings))
	}
	if len(p.fp.Settings) > maxSettings {
		t.Errorf("retained %d settings, past the %d cap", len(p.fp.Settings), maxSettings)
	}
	// Giving up must release what was accumulated, not just stop adding.
	if p.fp.Settings != nil || p.buf != nil || p.headerBlk != nil {
		t.Errorf("state retained after abandoning: settings=%d buf=%d hdr=%d",
			len(p.fp.Settings), len(p.buf), len(p.headerBlk))
	}
}

func TestPriorityFloodIsBounded(t *testing.T) {
	var p parser
	p.sawPreface = true

	frame := append([]byte{0, 0, 5, framePriority, 0, 0, 0, 0, 1},
		0x00, 0x00, 0x00, 0x00, 200)
	for i := 0; i < maxPriorities*4 && !p.give; i++ {
		p.feed(frame)
	}

	if !p.give {
		t.Fatalf("parser still accumulating after %d PRIORITY frames", maxPriorities*4)
	}
	if p.fp.Priorities != nil {
		t.Errorf("retained %d priorities after abandoning", len(p.fp.Priorities))
	}
}

// Abandoning must be sticky: later frames on a written-off connection are
// ignored rather than restarting accumulation.
func TestAbandonIsSticky(t *testing.T) {
	var p parser
	p.sawPreface = true
	p.abandon()

	p.feed(settingsFrame(10))
	if len(p.fp.Settings) != 0 {
		t.Errorf("accumulated %d settings after abandoning", len(p.fp.Settings))
	}
}

// A hostile pseudo-header name must not put arbitrary bytes in the output.
func TestOnlyDefinedPseudoHeadersAreCounted(t *testing.T) {
	var p parser
	p.sawPreface = true

	var block []byte
	add := func(name, value string) {
		block = append(block, 0x00, byte(len(name)))
		block = append(block, name...)
		block = append(block, byte(len(value)))
		block = append(block, value...)
	}
	add(":method", "GET")
	add(":\x07evil", "x")
	add(":protocol", "websocket")
	add(":path", "/")

	frame := append([]byte{
		byte(len(block) >> 16), byte(len(block) >> 8), byte(len(block)),
		frameHeaders, flagEndHeaders, 0, 0, 0, 1,
	}, block...)
	p.feed(frame)

	if !p.done {
		t.Fatal("headers were not parsed")
	}
	got := strings.Join(p.fp.PseudoHeaderOrder, ",")
	if got != "m,p" {
		t.Errorf("pseudo_header_order = %q, want %q (:\\x07evil and :protocol excluded)", got, "m,p")
	}
}
