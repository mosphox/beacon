package geoip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// The fixtures in testdata are written by testdata/mmdbgen, in the record
// shapes the real files use: flat maps, and IPLocate's ASN as a decimal string.
// Reading them through geoip2-golang would return empty records without an
// error, which is the failure these tests exist to catch.

func TestIPLocateReadsFlatRecords(t *testing.T) {
	p := NewIPLocate("testdata", nil)
	if err := p.Open(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for _, tc := range []struct {
		ip   string
		want Record
	}{
		{"8.8.8.8", Record{
			CountryCode: "US", Country: "United States", ContinentCode: "NA", Continent: "North America",
			ASN: 15169, ASNOrg: "Google LLC", HasData: true,
		}},
		{"2001:4860::8888", Record{
			CountryCode: "US", Country: "United States", ContinentCode: "NA", Continent: "North America",
			HasData: true,
		}},
		// No organisation, so the registry handle stands in for it.
		{"1.1.1.1", Record{ASN: 13335, ASNOrg: "CLOUDFLARENET", HasData: true}},
		// "EU" is a region: no country, and nothing else known.
		{"203.0.113.9", Record{}},
		{"10.0.0.1", Record{}},
	} {
		if got := p.Lookup(net.ParseIP(tc.ip)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.ip, got, tc.want)
		}
	}
}

func TestIPLocationDBReportsTheCodeAlone(t *testing.T) {
	p := NewIPLocationDB("testdata", nil)
	if err := p.Open(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for ip, want := range map[string]Record{
		// A code and nothing else: the source names no country, so beacon
		// does not name one on its behalf.
		"8.8.8.8":     {CountryCode: "US", HasData: true},
		"77.88.8.8":   {CountryCode: "RU", HasData: true},
		"2a01:4f8::1": {CountryCode: "DE", HasData: true},
		"10.0.0.1":    {},
	} {
		if got := p.Lookup(net.ParseIP(ip)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, want %+v", ip, got, want)
		}
	}
}

func TestParseLFSPointer(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	good := "version https://git-lfs.github.com/spec/v1\noid sha256:" + strings.ToUpper(hash) + "\nsize 17068536\n"
	ptr, err := parseLFSPointer(good)
	if err != nil {
		t.Fatalf("parseLFSPointer: %v", err)
	}
	if ptr.sha256 != hash || ptr.size != 17068536 {
		t.Errorf("got %+v, want sha256 %s size 17068536", ptr, hash)
	}

	for name, text := range map[string]string{
		"no version":   "oid sha256:" + hash + "\nsize 1\n",
		"short hash":   "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 1\n",
		"other digest": "version https://git-lfs.github.com/spec/v1\noid sha1:" + hash + "\nsize 1\n",
		"no size":      "version https://git-lfs.github.com/spec/v1\noid sha256:" + hash + "\n",
		"the database": "\x00\x01binary",
	} {
		if _, err := parseLFSPointer(text); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// lfsServer serves Git LFS pointers and objects the way GitHub splits them
// between raw.githubusercontent.com and media.githubusercontent.com. Serving
// an object counts, so a test can see whether a download happened at all.
type lfsServer struct {
	*httptest.Server
	objects map[string][]byte // what the pointer describes
	served  map[string][]byte // what is actually sent, if different
	fetches atomic.Int32
}

func newLFSServer(t *testing.T, objects map[string][]byte) *lfsServer {
	s := &lfsServer{objects: objects, served: map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path, ok := strings.CutPrefix(r.URL.Path, "/pointer/"); ok {
			obj, found := s.objects[path]
			if !found {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, "version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", sha256Hex(obj), len(obj))
			return
		}
		if path, ok := strings.CutPrefix(r.URL.Path, "/object/"); ok {
			s.fetches.Add(1)
			body, found := s.served[path]
			if !found {
				body = s.objects[path]
			}
			w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *lfsServer) provider(dir string) *IPLocate {
	p := NewIPLocate(dir, s.Client())
	p.pointerBase = s.URL + "/pointer/"
	p.objectBase = s.URL + "/object/"
	return p
}

func TestIPLocateDownloadsOnlyWhatChanged(t *testing.T) {
	dir := t.TempDir()
	srv := newLFSServer(t, map[string][]byte{
		iplocateCountryPath: []byte("country v1"),
		iplocateASNPath:     []byte("asn v1"),
	})
	p := srv.provider(dir)
	countryPath, asnPath := p.paths()

	if err := p.Download(); err != nil {
		t.Fatalf("first download: %v", err)
	}
	if b, _ := os.ReadFile(countryPath); string(b) != "country v1" {
		t.Errorf("country = %q, want %q", b, "country v1")
	}
	if b, _ := os.ReadFile(asnPath); string(b) != "asn v1" {
		t.Errorf("asn = %q, want %q", b, "asn v1")
	}

	// Pointers unchanged: nothing is fetched.
	srv.fetches.Store(0)
	if err := p.Download(); !errors.Is(err, errNotModified) {
		t.Errorf("second download = %v, want errNotModified", err)
	}
	if n := srv.fetches.Load(); n != 0 {
		t.Errorf("fetched %d objects for unchanged pointers, want none", n)
	}

	// One file moves on: only it is fetched and replaced.
	srv.objects[iplocateASNPath] = []byte("asn v2")
	if err := p.Download(); err != nil {
		t.Fatalf("third download: %v", err)
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Errorf("fetched %d objects, want only the changed one", n)
	}
	if b, _ := os.ReadFile(asnPath); string(b) != "asn v2" {
		t.Errorf("asn = %q after update, want %q", b, "asn v2")
	}
}

// What arrives must be what the pointer describes, or nothing is installed.
func TestIPLocateRejectsAnObjectThatDoesNotMatchItsPointer(t *testing.T) {
	for name, mutate := range map[string]func(s *lfsServer){
		"different bytes": func(s *lfsServer) { s.served[iplocateASNPath] = []byte("asn v9") },
		"truncated":       func(s *lfsServer) { s.served[iplocateASNPath] = []byte("asn") },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			srv := newLFSServer(t, map[string][]byte{
				iplocateCountryPath: []byte("country v1"),
				iplocateASNPath:     []byte("asn v1"),
			})
			mutate(srv)
			p := srv.provider(dir)

			if err := p.Download(); err == nil {
				t.Fatal("download succeeded with a mismatched object")
			}
			countryPath, asnPath := p.paths()
			for _, path := range []string{countryPath, asnPath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("%s was installed from a failed download", filepath.Base(path))
				}
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
				t.Errorf("temp files left behind: %v", left)
			}
		})
	}
}

func TestIPLocationDBDownloadChecksTheChecksum(t *testing.T) {
	db := []byte("user-country v1")
	sum := sha256Hex(db)
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sum":
			fmt.Fprintf(w, "%s  user-country.mmdb\n", sum)
		case "/db":
			fetches.Add(1)
			w.Write(db)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := NewIPLocationDB(dir, srv.Client())
	p.url, p.sumURL = srv.URL+"/db", srv.URL+"/sum"

	if err := p.Download(); err != nil {
		t.Fatalf("first download: %v", err)
	}
	if b, _ := os.ReadFile(p.path()); string(b) != "user-country v1" {
		t.Errorf("installed %q", b)
	}

	fetches.Store(0)
	if err := p.Download(); !errors.Is(err, errNotModified) {
		t.Errorf("unchanged checksum: got %v, want errNotModified", err)
	}
	if n := fetches.Load(); n != 0 {
		t.Errorf("fetched the database %d times for an unchanged checksum", n)
	}

	// The checksum moves on but the file has not yet: the gap between the two
	// release uploads. The installed file must survive it.
	sum = strings.Repeat("0", 64)
	if err := p.Download(); err == nil {
		t.Fatal("installed a database that does not match its checksum")
	}
	if b, _ := os.ReadFile(p.path()); string(b) != "user-country v1" {
		t.Errorf("installed file = %q after a failed update, want the previous one", b)
	}

	sum = "not a checksum"
	if err := p.Download(); err == nil {
		t.Error("accepted a checksum file with no checksum in it")
	}
}
