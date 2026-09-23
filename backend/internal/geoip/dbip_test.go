package geoip

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"path"
	"sync"
	"testing"
	"time"
)

// dbipServer publishes month-stamped files the way download.db-ip.com does,
// with Last-Modified on both GET and HEAD.
type dbipServer struct {
	t *testing.T

	mu    sync.Mutex
	files map[string]time.Time // file name → published
	gets  int
}

func (s *dbipServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := path.Base(r.URL.Path)
	published, ok := s.files[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		s.gets++
	}
	http.ServeContent(w, r, "", published, bytes.NewReader(gzipped(s.t, name)))
}

func (s *dbipServer) publish(name string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = at
}

func (s *dbipServer) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

func newDBIPServer(t *testing.T) (*dbipServer, *DBIP) {
	s := &dbipServer{t: t, files: map[string]time.Time{}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	d := NewDBIP(t.TempDir(), srv.Client())
	d.baseURL = srv.URL + "/free"
	return s, d
}

// DB-IP publishes once a month. A check finds the file installed is the one
// published and downloads nothing; a new month's file is downloaded.
func TestDBIPDownloadsOnlyANewMonth(t *testing.T) {
	s, d := newDBIPServer(t)
	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := thisMonth.AddDate(0, -1, 0)
	for _, ed := range []string{"city", "asn"} {
		s.publish("dbip-"+ed+"-lite-"+last.Format("2006-01")+".mmdb.gz", last.Add(6*time.Hour))
	}

	// This month's is not out yet: last month's is used.
	if err := d.Download(); err != nil {
		t.Fatal(err)
	}
	city, asn := d.paths()
	if got := installed(t, city); got != "dbip-city-lite-"+last.Format("2006-01")+".mmdb.gz" {
		t.Fatalf("installed %q, want last month's City", got)
	}
	if s.getCount() != 2 {
		t.Fatalf("%d downloads, want 2", s.getCount())
	}

	if err := d.Download(); !errors.Is(err, errNotModified) {
		t.Fatalf("same month: Download() = %v, want errNotModified", err)
	}
	if s.getCount() != 2 {
		t.Errorf("the installed month was downloaded again: %d downloads", s.getCount())
	}

	for _, ed := range []string{"city", "asn"} {
		s.publish("dbip-"+ed+"-lite-"+thisMonth.Format("2006-01")+".mmdb.gz", thisMonth.Add(6*time.Hour))
	}
	if err := d.Download(); err != nil {
		t.Fatal(err)
	}
	if got := installed(t, asn); got != "dbip-asn-lite-"+thisMonth.Format("2006-01")+".mmdb.gz" {
		t.Errorf("installed %q, want this month's ASN", got)
	}
	if s.getCount() != 4 {
		t.Errorf("%d downloads after a new month, want 4", s.getCount())
	}
}

// The month before is counted from the first: from the 31st of March,
// AddDate would land on the 3rd of March and ask for March again.
func TestDBIPFallsBackToTheCalendarMonthBefore(t *testing.T) {
	s, d := newDBIPServer(t)
	s.publish("dbip-city-lite-2027-02.mmdb.gz", time.Date(2027, 2, 1, 6, 0, 0, 0, time.UTC))
	url, _, err := d.latest("city", time.Date(2027, 3, 31, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if path.Base(url) != "dbip-city-lite-2027-02.mmdb.gz" {
		t.Errorf("fell back to %s, want February's", path.Base(url))
	}
}
