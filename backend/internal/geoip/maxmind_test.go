package geoip

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// maxmindServer publishes GeoLite2 editions the way download.maxmind.com
// does: an archive and its SHA-256 at each edition's permalink, behind basic
// auth, with the build's Last-Modified on both GET and HEAD.
type maxmindServer struct {
	t *testing.T

	mu       sync.Mutex
	builds   map[string]time.Time // edition → published
	content  map[string]string    // edition → the .mmdb inside the archive
	badSum   map[string]bool
	archives map[string]int // GETs of each edition's archive
}

func newMaxMindServer(t *testing.T) (*maxmindServer, *MaxMind) {
	day := time.Date(2026, 9, 22, 22, 15, 0, 0, time.UTC)
	s := &maxmindServer{
		t:        t,
		builds:   map[string]time.Time{},
		content:  map[string]string{},
		badSum:   map[string]bool{},
		archives: map[string]int{},
	}
	for _, ed := range []string{mmCountryEdition, mmCityEdition, mmASNEdition} {
		s.builds[ed], s.content[ed] = day, ed+" build 1"
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	m := NewMaxMind("42", "s3cret", t.TempDir(), srv.Client())
	m.baseURL = srv.URL + "/geoip/databases"
	return s, m
}

func (s *maxmindServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.RawQuery, "s3cret") {
		s.t.Errorf("licence key in the URL: %s", r.URL)
	}
	if user, pass, ok := r.BasicAuth(); !ok || user != "42" || pass != "s3cret" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	edition := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/geoip/databases/"), "/download")

	s.mu.Lock()
	defer s.mu.Unlock()
	built, ok := s.builds[edition]
	if !ok {
		http.NotFound(w, r)
		return
	}
	archive := s.archive(edition)
	switch r.URL.Query().Get("suffix") {
	case "tar.gz":
		if r.Method == http.MethodGet {
			s.archives[edition]++
		}
		http.ServeContent(w, r, "", built, bytes.NewReader(archive))
	case "tar.gz.sha256":
		sum := sha256.Sum256(archive)
		if s.badSum[edition] {
			sum[0]++
		}
		line := fmt.Sprintf("%s  %s_%s.tar.gz\n", hex.EncodeToString(sum[:]), edition, built.Format("20060102"))
		http.ServeContent(w, r, "", built, strings.NewReader(line))
	default:
		http.NotFound(w, r)
	}
}

func (s *maxmindServer) archive(edition string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	body := s.content[edition]
	dir := edition + "_" + s.builds[edition].Format("20060102")
	for _, h := range []*tar.Header{
		{Name: dir + "/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: dir + "/COPYRIGHT.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2},
		{Name: dir + "/" + edition + ".mmdb", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))},
	} {
		if err := tw.WriteHeader(h); err != nil {
			s.t.Fatal(err)
		}
		switch {
		case strings.HasSuffix(h.Name, ".txt"):
			tw.Write([]byte("c\n"))
		case strings.HasSuffix(h.Name, ".mmdb"):
			tw.Write([]byte(body))
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func (s *maxmindServer) publish(edition, body string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.builds[edition], s.content[edition] = at, body
}

func (s *maxmindServer) gets() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for k, v := range s.archives {
		out[k] = v
	}
	return out
}

func installed(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Only an edition whose build has moved is downloaded: the rest are checked
// with HEAD requests, which MaxMind does not count.
func TestMaxMindDownloadsOnlyEditionsThatMoved(t *testing.T) {
	s, m := newMaxMindServer(t)
	country, city, asn := m.paths()

	if err := m.Download(); err != nil {
		t.Fatal(err)
	}
	if got := s.gets(); got[mmCountryEdition] != 1 || got[mmCityEdition] != 1 || got[mmASNEdition] != 1 {
		t.Fatalf("first download fetched %v, want each edition once", got)
	}
	if !isRelease(city, s.builds[mmCityEdition]) {
		t.Error("installed City does not record the build it holds")
	}

	if err := m.Download(); !errors.Is(err, errNotModified) {
		t.Fatalf("unchanged editions: Download() = %v, want errNotModified", err)
	}
	if got := s.gets(); got[mmCityEdition] != 1 || got[mmASNEdition] != 1 {
		t.Errorf("unchanged editions downloaded again: %v", got)
	}

	s.publish(mmASNEdition, "GeoLite2-ASN build 2", time.Date(2026, 9, 23, 8, 30, 11, 0, time.UTC))
	if err := m.Download(); err != nil {
		t.Fatal(err)
	}
	if got := s.gets(); got[mmASNEdition] != 2 || got[mmCityEdition] != 1 || got[mmCountryEdition] != 1 {
		t.Errorf("after a new ASN build, fetched %v; want ASN twice and the others once", got)
	}
	if installed(t, asn) != "GeoLite2-ASN build 2" || installed(t, country) != "GeoLite2-Country build 1" {
		t.Errorf("installed ASN %q, Country %q", installed(t, asn), installed(t, country))
	}
}

// A new build that fails its checksum installs nothing, not even the other
// edition fetched in the same refresh.
func TestMaxMindInstallsNothingWhenAChecksumFails(t *testing.T) {
	s, m := newMaxMindServer(t)
	_, city, asn := m.paths()
	if err := m.Download(); err != nil {
		t.Fatal(err)
	}

	next := time.Date(2026, 9, 25, 22, 15, 0, 0, time.UTC)
	s.publish(mmCityEdition, "GeoLite2-City build 2", next)
	s.publish(mmASNEdition, "GeoLite2-ASN build 2", next)
	s.mu.Lock()
	s.badSum[mmASNEdition] = true
	s.mu.Unlock()

	if err := m.Download(); err == nil || errors.Is(err, errNotModified) {
		t.Fatalf("Download() = %v, want a checksum failure", err)
	}
	if installed(t, city) != "GeoLite2-City build 1" || installed(t, asn) != "GeoLite2-ASN build 1" {
		t.Errorf("a failed refresh installed City %q, ASN %q", installed(t, city), installed(t, asn))
	}
	for _, p := range []string{city, asn} {
		if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
			t.Errorf("temp file left behind for %s", p)
		}
	}
}
