package geoip

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

func xzCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ipfireServer serves one compressed database with a Last-Modified, and
// answers 304 to a request that already has it, as location.ipfire.org does.
func ipfireServer(t *testing.T, body []byte, modified time.Time) (*httptest.Server, *int) {
	full := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if since, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil && !modified.After(since) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full++
		w.Header().Set("Last-Modified", modified.UTC().Format(http.TimeFormat))
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &full
}

func testIPFire(dir string, srv *httptest.Server, key *ecdsa.PublicKey) *IPFire {
	p := NewIPFire(dir, srv.Client())
	p.url = srv.URL
	p.key = key
	return p
}

func TestIPFireDownloadVerifiesInstallsAndThenAsksConditionally(t *testing.T) {
	key := testKey(t)
	modified := time.Date(2026, 9, 23, 4, 40, 41, 0, time.UTC)
	srv, full := ipfireServer(t, xzCompress(t, sampleLocDB.build(t, key, 1)), modified)

	dir := t.TempDir()
	p := testIPFire(dir, srv, &key.PublicKey)
	if err := p.Download(); err != nil {
		t.Fatalf("download: %v", err)
	}
	fi, err := os.Stat(p.path())
	if err != nil {
		t.Fatalf("nothing installed: %v", err)
	}
	// The installed file carries the server's Last-Modified, which is what the
	// next request sends back.
	if !fi.ModTime().Equal(modified) {
		t.Errorf("installed file's mtime = %v, want Last-Modified %v", fi.ModTime(), modified)
	}

	if err := p.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer p.Close()
	if rec := p.Lookup(net.ParseIP("10.200.0.1")); !rec.IsAnycast || rec.CountryCode != "US" {
		t.Errorf("lookup = %+v, want the anycast US network", rec)
	}

	if err := p.Download(); !errors.Is(err, errNotModified) {
		t.Errorf("second download = %v, want errNotModified", err)
	}
	if *full != 1 {
		t.Errorf("served the database %d times, want once", *full)
	}
}

// The signature, not the connection it came over, decides what is installed.
func TestIPFireRejectsADatabaseItCannotVerify(t *testing.T) {
	key, stranger := testKey(t), testKey(t)
	for name, body := range map[string][]byte{
		"signed by another key": xzCompress(t, sampleLocDB.build(t, stranger, 1)),
		"unsigned":              xzCompress(t, sampleLocDB.build(t, nil, 0)),
		"not a database":        xzCompress(t, []byte("hello")),
		"not xz":                sampleLocDB.build(t, key, 1),
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := ipfireServer(t, body, time.Now())
			dir := t.TempDir()
			p := testIPFire(dir, srv, &key.PublicKey)

			if err := p.Download(); err == nil {
				t.Fatal("download succeeded")
			}
			if _, err := os.Stat(p.path()); !os.IsNotExist(err) {
				t.Error("an unverified database was installed")
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
				t.Errorf("temp files left behind: %v", left)
			}
		})
	}
}

func TestLZMA2DictSize(t *testing.T) {
	for bits, want := range map[byte]int64{
		0:  4 << 10,
		1:  6 << 10,
		18: 2 << 20,
		22: 8 << 20,
		28: 64 << 20, // IPFire's
		30: 128 << 20,
		31: 192 << 20,
		40: 0xFFFFFFFF,
	} {
		if got := lzma2DictSize(bits); got != want {
			t.Errorf("lzma2DictSize(%d) = %d, want %d", bits, got, want)
		}
	}
	if got := lzma2DictSize(41); got <= maxXZDictBytes {
		t.Errorf("an invalid property decoded to %d, which would be accepted", got)
	}
}

// xzHeaders builds a stream header and a one-filter LZMA2 block header asking
// for the dictionary the property byte encodes. Nothing after it is needed:
// the check runs before any decoding.
func xzHeaders(dictBits byte) []byte {
	stream := []byte{0xFD, '7', 'z', 'X', 'Z', 0x00, 0x00, 0x04, 0, 0, 0, 0}
	// size byte (2 → 12 bytes), flags (one filter, no sizes), filter id 0x21,
	// one property byte, the property, padding, CRC32.
	block := []byte{0x02, 0x00, 0x21, 0x01, dictBits, 0x00, 0x00, 0x00, 0, 0, 0, 0}
	return append(stream, block...)
}

func TestCheckXZDict(t *testing.T) {
	peek := func(b []byte) *bufio.Reader { return bufio.NewReaderSize(bytes.NewReader(b), xzHeaderPeek) }

	if err := checkXZDict(peek(xzHeaders(28)), maxXZDictBytes); err != nil {
		t.Errorf("64 MiB dictionary refused: %v", err)
	}
	if err := checkXZDict(peek(xzHeaders(31)), maxXZDictBytes); err == nil {
		t.Error("192 MiB dictionary accepted")
	}
	if err := checkXZDict(peek(xzHeaders(40)), maxXZDictBytes); err == nil {
		t.Error("4 GiB dictionary accepted")
	}
	if err := checkXZDict(peek([]byte("LOCDBXX\x01 not compressed")), maxXZDictBytes); err == nil {
		t.Error("accepted a stream that is not xz")
	}

	// A real stream passes, and checking it consumes nothing the decoder needs.
	want := sampleLocDB.build(t, nil, 0)
	br := peek(xzCompress(t, want))
	if err := checkXZDict(br, maxXZDictBytes); err != nil {
		t.Fatalf("real stream refused: %v", err)
	}
	xr, err := xz.ReaderConfig{SingleStream: true}.NewReader(br)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(xr)
	if err != nil {
		t.Fatalf("decode after check: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("stream decoded differently after the header check")
	}
}
