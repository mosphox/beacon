package geoip

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseGeofeedFollowsRFC8805(t *testing.T) {
	feed := "\xef\xbb\xbf# comment\r\n" + `192.0.2.0/25,US,US-AL,,
192.0.2.5,US,US-AL,Alabaster,
2001:db8::/32,PL,,,
2001:db8:cafe::/48,PL,PL-MZ,,
192.0.2.64/26,,,,
198.51.100.0/24,ZZ,,,
203.0.113.0/24,se,se-m,Malmö # a comment after the entry
203.0.113.7/28,SE,,Stockholm
100.64.0.0/10,,SE-AB,Stockholm
"198.51.100.128/25","SE","","Göteborg"
10.0.0.0/8,USA,,,
10.0.0.0/8,US,CA-ON,,
10.0.0.0/8,US,US-TOOLONG,,
not-a-prefix,US,,,
192.0.2.0/33,US,,,
fe80::1%eth0,US,,,
::ffff:192.0.2.1,US,,,
192.0.2.0/24,US,,city` + "\x01" + `
`
	var got []string
	malformed := parseGeofeed([]byte(feed), func(pfx netip.Prefix, pl geoPlace) {
		got = append(got, fmt.Sprintf("%s %s/%s/%s", pfx, pl.cc, pl.region, pl.city))
	})
	want := []string{
		"192.0.2.0/25 US/AL/",
		"192.0.2.5/32 US/AL/Alabaster",
		"2001:db8::/32 PL//",
		"2001:db8:cafe::/48 PL/MZ/",
		"192.0.2.64/26 //",             // no location, stated
		"198.51.100.0/24 //",           // "ZZ" says the same
		"203.0.113.0/24 SE/M/Malmö",    // case-insensitive codes, UTF-8 city
		"203.0.113.0/28 SE//Stockholm", // host bits cleared
		"100.64.0.0/10 SE/AB/Stockholm",
		"198.51.100.128/25 SE//Göteborg",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if malformed != 8 {
		t.Errorf("%d malformed, want 8", malformed)
	}
}

// buildGeofeeds builds and opens an index from links and feed bodies, keyed
// by URL.
func buildGeofeeds(t *testing.T, links []geofeedLink, feeds map[string]string) (*geofeedsIndex, *geofeedBuild) {
	t.Helper()
	urls := linkedFeeds(links)
	b := newGeofeedBuild(links, urls)
	for i, u := range urls {
		if body, ok := feeds[u]; ok {
			b.addFeed(uint32(i+1), []byte(body))
		}
	}
	path := filepath.Join(t.TempDir(), "geofeeds.idx")
	if err := b.index([sha256.Size]byte{1}).write(path); err != nil {
		t.Fatal(err)
	}
	idx, err := openGeofeedsIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idx.close)
	return idx, b
}

func link(first, last, url string) geofeedLink {
	return geofeedLink{netip.MustParseAddr(first), netip.MustParseAddr(last), url}
}

func placeOf(idx *geofeedsIndex, ip string) string {
	rec := idx.lookup(net.ParseIP(ip))
	if !rec.HasData {
		return "-"
	}
	_, region := rec.Region()
	return rec.CountryCode + "/" + region + "/" + rec.City
}

// RFC 9632's example: a feed speaks only inside the block that links it,
// and not where a more specific block links a feed of its own.
func TestGeofeedSpeaksOnlyWhereItIsLinked(t *testing.T) {
	const wide, narrow = "https://a.example/wide", "https://b.example/narrow"
	idx, b := buildGeofeeds(t,
		[]geofeedLink{
			link("192.0.0.0", "192.0.3.255", wide), // 192.0.0.0/22
			link("192.0.2.0", "192.0.2.255", narrow),
		},
		map[string]string{
			wide: `192.0.0.0/22,SE,,Malmö
192.0.2.0/29,NO,,Oslo
198.51.100.0/24,US,,Chicago
`,
			narrow: `192.0.2.0/24,DE,,Berlin
`,
		})

	for ip, want := range map[string]string{
		"192.0.0.1":    "SE//Malmö",
		"192.0.3.254":  "SE//Malmö",
		"192.0.2.1":    "DE//Berlin", // the wide feed's /29 is ignored: a narrower block links its own feed
		"198.51.100.1": "-",          // outside every block that links the feed
		"203.0.113.1":  "-",
	} {
		if got := placeOf(idx, ip); got != want {
			t.Errorf("%s: got %s, want %s", ip, got, want)
		}
	}
	if b.used != 2 || b.outside != 2 {
		t.Errorf("%d used, %d outside; want 2 and 2", b.used, b.outside)
	}
}

// Within a feed the most specific entry answers, including one that says a
// range has no location, and a prefix listed twice keeps its first listing.
func TestGeofeedMostSpecificEntryAnswers(t *testing.T) {
	const feed = "https://c.example/feed"
	idx, b := buildGeofeeds(t,
		[]geofeedLink{link("2001:db8::", "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", feed)},
		map[string]string{feed: `2001:db8::/32,SE,SE-AB,Stockholm
2001:db8:1::/48,SE,SE-M,Malmö
2001:db8:1:2::/64,,,,
2001:db8:1::/48,NO,,Oslo
`})
	for ip, want := range map[string]string{
		"2001:db8::1":     "SE/AB/Stockholm",
		"2001:db8:1::1":   "SE/M/Malmö",
		"2001:db8:1:2::1": "-", // stated to have no location
		"2001:db8:1:3::1": "SE/M/Malmö",
	} {
		if got := placeOf(idx, ip); got != want {
			t.Errorf("%s: got %s, want %s", ip, got, want)
		}
	}
	if b.repeated != 1 {
		t.Errorf("%d repeated, want 1", b.repeated)
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8":           true,
		"2a00:1450:4001::1": true,
		"127.0.0.1":         false,
		"10.1.2.3":          false,
		"172.17.0.1":        false, // the Docker bridge
		"192.168.1.1":       false,
		"100.64.1.1":        false,
		"169.254.169.254":   false, // cloud metadata
		"0.0.0.0":           false,
		"240.0.0.1":         false,
		"255.255.255.255":   false,
		"224.0.0.1":         false,
		"::1":               false,
		"fc00::1":           false,
		"fe80::1":           false,
		"::ffff:127.0.0.1":  false,
		"64:ff9b::7f00:1":   false, // 127.0.0.1 through NAT64
		"2002:7f00:1::":     false, // and through 6to4
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("publicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestNextFetchHonoursThePublisherWithinLimits(t *testing.T) {
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			out.Set(kv[i], kv[i+1])
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"nothing said: weekly", h(), 7 * 24 * time.Hour},
		{"max-age of two days", h("Cache-Control", "public, max-age=172800"), 48 * time.Hour},
		{"max-age too short: twice a day at most", h("Cache-Control", "max-age=60"), 12 * time.Hour},
		{"Expires in three days", h("Expires", now.Add(72*time.Hour).Format(http.TimeFormat)), 72 * time.Hour},
		{"Expires in a month: still weekly", h("Expires", now.Add(30*24*time.Hour).Format(http.TimeFormat)), 7 * 24 * time.Hour},
		{"max-age over Expires", h("Cache-Control", "max-age=86400", "Expires", now.Add(72*time.Hour).Format(http.TimeFormat)), 24 * time.Hour},
	} {
		if got := nextFetch(tc.header, now).Sub(now); got != tc.want {
			t.Errorf("%s: next in %s, want %s", tc.name, got, tc.want)
		}
	}
}

// feedServer publishes geofeeds over HTTPS with ETags, and records what it
// was asked.
type feedServer struct {
	mu       sync.Mutex
	bodies   map[string]string
	status   map[string]int
	requests []string // path, and "conditional" when it was
	srv      *httptest.Server
}

func newFeedServer(t *testing.T) *feedServer {
	s := &feedServer{bodies: map[string]string{}, status: map[string]int{}}
	s.srv = httptest.NewTLSServer(s)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *feedServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.bodies[r.URL.Path]
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256([]byte(body)))
	conditional := r.Header.Get("If-None-Match") != ""
	s.requests = append(s.requests, fmt.Sprintf("%s conditional=%v", r.URL.Path, conditional))
	switch {
	case s.status[r.URL.Path] != 0:
		w.WriteHeader(s.status[r.URL.Path])
	case !ok:
		http.NotFound(w, r)
	case r.Header.Get("If-None-Match") == etag:
		w.WriteHeader(http.StatusNotModified)
	default:
		w.Header().Set("ETag", etag)
		w.Write([]byte(body))
	}
}

func (s *feedServer) url(path string) string { return s.srv.URL + path }

func (s *feedServer) set(path, body string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies[path], s.status[path] = body, status
}

func (s *feedServer) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.requests
	s.requests = nil
	return out
}

func (s *feedServer) roots() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(s.srv.Certificate())
	return pool
}

func writeLinks(t *testing.T, dir string, lines ...string) {
	t.Helper()
	data := "# test links\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(geofeedLinksPath(dir), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeDue brings every feed's next fetch forward to now, as a week passing
// would.
func makeDue(t *testing.T, p *Geofeeds) {
	t.Helper()
	state := p.loadState()
	for _, st := range state {
		st.Next = time.Time{}
	}
	if err := p.saveState(state); err != nil {
		t.Fatal(err)
	}
}

func openedPlace(t *testing.T, p *Geofeeds, ip string) string {
	t.Helper()
	if err := p.Open(); err != nil {
		t.Fatal(err)
	}
	rec := p.Lookup(net.ParseIP(ip))
	if !rec.HasData {
		return "-"
	}
	return rec.CountryCode + "/" + rec.City
}

func TestGeofeedsFetchPolitelyAndRebuildOnlyOnChange(t *testing.T) {
	s := newFeedServer(t)
	s.set("/a.csv", "192.0.2.0/24,SE,,Malmö\n", 0)
	s.set("/b.csv", "2001:db8::/32,DE,,Berlin\n", 0)
	dir := t.TempDir()
	writeLinks(t, dir,
		"192.0.2.0\t192.0.2.255\t"+s.url("/a.csv"),
		"2001:db8::\t2001:db8:ffff:ffff:ffff:ffff:ffff:ffff\t"+s.url("/b.csv"),
	)
	p := newGeofeeds(dir, func(netip.Addr) bool { return true }, s.roots())
	t.Cleanup(p.Close)

	if err := p.Download(); err != nil {
		t.Fatal(err)
	}
	if got := openedPlace(t, p, "192.0.2.10"); got != "SE/Malmö" {
		t.Errorf("192.0.2.10: %s", got)
	}
	if got := openedPlace(t, p, "2001:db8::1"); got != "DE/Berlin" {
		t.Errorf("2001:db8::1: %s", got)
	}
	if got := s.asked(); len(got) != 2 {
		t.Fatalf("first pass asked %v, want both feeds once", got)
	}

	// Not due for a week: nothing is asked, nothing rebuilt.
	if err := p.Download(); !errors.Is(err, errNotModified) {
		t.Fatalf("second pass: %v, want errNotModified", err)
	}
	if got := s.asked(); len(got) != 0 {
		t.Errorf("asked again within the week: %v", got)
	}

	// A week on, the check is conditional and costs a 304.
	makeDue(t, p)
	if err := p.Download(); !errors.Is(err, errNotModified) {
		t.Fatalf("unchanged feeds: %v, want errNotModified", err)
	}
	if got := strings.Join(s.asked(), " "); strings.Count(got, "conditional=true") != 2 {
		t.Errorf("a week on, asked %s; want two conditional requests", got)
	}

	// A feed that changes is picked up.
	s.set("/b.csv", "2001:db8::/32,DE,,Hamburg\n", 0)
	makeDue(t, p)
	if err := p.Download(); err != nil {
		t.Fatal(err)
	}
	if got := openedPlace(t, p, "2001:db8::1"); got != "DE/Hamburg" {
		t.Errorf("after the feed changed: %s", got)
	}

	// A feed that fails keeps its last good copy, and is left a day.
	s.set("/a.csv", "", http.StatusInternalServerError)
	makeDue(t, p)
	if err := p.Download(); !errors.Is(err, errNotModified) {
		t.Fatalf("a failing feed: %v, want errNotModified", err)
	}
	if got := openedPlace(t, p, "192.0.2.10"); got != "SE/Malmö" {
		t.Errorf("after a failed fetch: %s", got)
	}
	st := p.loadState()[s.url("/a.csv")]
	if st.Failures != 1 || time.Until(st.Next) < 23*time.Hour {
		t.Errorf("failed feed state: %+v", st)
	}

	// A feed no longer linked is dropped, from the index and the disk.
	writeLinks(t, dir, "2001:db8::\t2001:db8:ffff:ffff:ffff:ffff:ffff:ffff\t"+s.url("/b.csv"))
	if err := p.Download(); err != nil {
		t.Fatal(err)
	}
	if got := openedPlace(t, p, "192.0.2.10"); got != "-" {
		t.Errorf("an unlinked feed still answers: %s", got)
	}
	if filesPresent(p.cachePath(s.url("/a.csv"))) {
		t.Error("an unlinked feed's copy is still on disk")
	}
}

// Links come from records anyone can create. A feed on a private or loopback
// address is never fetched, whatever the link says.
func TestGeofeedsRefusePrivateAddresses(t *testing.T) {
	s := newFeedServer(t)
	s.set("/a.csv", "192.0.2.0/24,SE,,Malmö\n", 0)
	dir := t.TempDir()
	writeLinks(t, dir, "192.0.2.0\t192.0.2.255\t"+s.url("/a.csv"))
	p := newGeofeeds(dir, publicAddr, s.roots())

	err := p.Download()
	if err == nil || !strings.Contains(err.Error(), "no linked feed") {
		t.Fatalf("Download() = %v, want no feed fetched", err)
	}
	if got := s.asked(); len(got) != 0 {
		t.Errorf("the loopback server was asked %v", got)
	}
	if st := p.loadState()[s.url("/a.csv")]; st == nil || !strings.Contains(st.Error, "not a public address") {
		t.Errorf("feed state = %+v, want the refusal recorded", st)
	}
}

func TestGeofeedsFollowOnlyHTTPS(t *testing.T) {
	s := newFeedServer(t)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("192.0.2.0/24,SE,,Malmö\n"))
	}))
	t.Cleanup(plain.Close)
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/a.csv", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	pool := s.roots()
	pool.AddCert(redirect.Certificate())

	if validGeofeedURL(plain.URL+"/a.csv") || validGeofeedURL("ftp://example.com/feed") ||
		validGeofeedURL("https://user:pass@example.com/feed") || !validGeofeedURL("https://example.com/feed") {
		t.Error("validGeofeedURL accepts something other than HTTPS, or refuses HTTPS")
	}

	dir := t.TempDir()
	writeLinks(t, dir,
		"192.0.2.0\t192.0.2.255\t"+redirect.URL+"/a.csv",
		"198.51.100.0\t198.51.100.255\t"+plain.URL+"/a.csv", // not read at all
	)
	links, _, err := readGeofeedLinks(geofeedLinksPath(dir))
	if err != nil || len(links) != 1 {
		t.Fatalf("read %d links (%v), want the HTTPS one only", len(links), err)
	}
	p := newGeofeeds(dir, func(netip.Addr) bool { return true }, pool)
	if err := p.Download(); err == nil {
		t.Fatal("a feed redirected to plain HTTP was accepted")
	}
	if st := p.loadState()[redirect.URL+"/a.csv"]; st == nil || !strings.Contains(st.Error, "HTTPS") {
		t.Errorf("feed state = %+v, want the redirect refused", st)
	}
}

func TestGeofeedsIndexRefusesDamage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "geofeeds.idx")
	d := &geofeedsIndexData{
		starts4: []v4addr{10, 20}, ids4: []uint32{1, 0},
		starts6: []v6addr{{1, 0}}, ids6: []uint32{2},
		places: []geoPlace{{cc: "SE", region: "M", city: "Malmö"}, {cc: "DE"}},
	}
	if err := d.write(path); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseGeofeedsIndex(good); err != nil {
		t.Fatalf("a good index refused: %v", err)
	}
	badID := bytes.Clone(good)
	be.PutUint32(badID[geofeedsIndexHeader+2*4:], 9) // the first IPv4 range names place 9
	for name, bad := range map[string][]byte{
		"empty":         nil,
		"wrong magic":   append([]byte("BGEOFIX0"), good[8:]...),
		"truncated":     good[:len(good)-1],
		"trailing":      append(bytes.Clone(good), 0),
		"missing place": badID,
	} {
		if _, err := parseGeofeedsIndex(bad); err == nil {
			t.Errorf("%s index accepted", name)
		}
	}
}
