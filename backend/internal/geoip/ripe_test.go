package geoip

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseRPSLTakesEachObjectsKeyAndFirstCountry(t *testing.T) {
	dump := `#
# The contents of this file are subject to
# RIPE Database Terms and Conditions
#

inetnum:        192.0.2.0 - 192.0.2.255
netname:        EXAMPLE
country:        nl # lower case, and a comment
country:        DE
remarks:        a continuation line is not an attribute
                country: XX
+               nor is this

inetnum:        198.51.100.0 - 198.51.100.255
netname:        NO-COUNTRY
inetnum:        203.0.113.0 - 203.0.113.255
country:        EU

route:          192.0.2.0/24
country:        FR

inetnum:        100.64.0.0 - 100.127.255.255
country:        US`

	type object struct{ key, country string }
	var got []object
	err := parseRPSL(strings.NewReader(dump), "inetnum", func(o *rpslObject) {
		got = append(got, object{string(o.key), string(o.country)})
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []object{
		{"192.0.2.0 - 192.0.2.255", "nl"},
		{"198.51.100.0 - 198.51.100.255", ""}, // ended by the next key, not a blank line
		{"203.0.113.0 - 203.0.113.255", "EU"},
		{"100.64.0.0 - 100.127.255.255", "US"}, // the route's country is not carried over
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

// Both forms of a geofeed link are read, the attribute winning over the
// remark when an object has both, as RFC 9632 requires.
func TestParseRPSLReadsGeofeedLinks(t *testing.T) {
	dump := `inetnum:        192.0.2.0 - 192.0.2.255
remarks:        Geofeed https://old.example/feed.csv
geofeed:        https://new.example/feed.csv # a comment
country:        SE

inetnum:        198.51.100.0 - 198.51.100.255
remarks:        Some other remark
remarks:        Geofeed https://example.net/geofeed#section
remarks:        Geofeed https://example.net/second

inetnum:        203.0.113.0 - 203.0.113.255
remarks:        geofeed https://lower.example/ignored

inetnum:        100.64.0.0 - 100.64.0.255

inetnum:        100.65.0.0 - 100.65.0.255
geofeed:        https://attribute.example/first
remarks:        Geofeed https://remark.example/after
`
	var got []string
	err := parseRPSL(strings.NewReader(dump), "inetnum", func(o *rpslObject) {
		got = append(got, string(o.key)+" -> "+string(o.geofeed))
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"192.0.2.0 - 192.0.2.255 -> https://new.example/feed.csv",
		"198.51.100.0 - 198.51.100.255 -> https://example.net/geofeed#section", // the first one, "#" and all
		"203.0.113.0 - 203.0.113.255 -> ",                                      // the token is case sensitive
		"100.64.0.0 - 100.64.0.255 -> ",
		"100.65.0.0 - 100.65.0.255 -> https://attribute.example/first", // after a remark-only object, still the attribute
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCountryOfABlock(t *testing.T) {
	for in, want := range map[string]string{"DE": "DE", "nl": "NL", "EU": "", "": "", "ZZ": "", "DEU": ""} {
		cc := ccOf([]byte(in))
		if got := strings.TrimRight(string(cc[:]), "\x00"); got != want {
			t.Errorf("ccOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRange4(t *testing.T) {
	for _, tc := range []struct {
		key        string
		start, end string // empty when the key must be refused
	}{
		{"192.0.2.0 - 192.0.2.255", "192.0.2.0", "192.0.2.255"},
		{"192.0.2.7-192.0.2.7", "192.0.2.7", "192.0.2.7"},
		{"0.0.0.0 - 255.255.255.255", "0.0.0.0", "255.255.255.255"},
		{"192.0.2.255 - 192.0.2.0", "", ""}, // backwards
		{"192.0.2.0", "", ""},
		{"192.0.2.0 - 2001:db8::", "", ""},
		{"192.0.2.0 - 192.0.2", "", ""},
	} {
		start, end, ok := parseRange4([]byte(tc.key))
		if tc.start == "" {
			if ok {
				t.Errorf("parseRange4(%q) accepted", tc.key)
			}
			continue
		}
		if !ok || ip4(start) != tc.start || ip4(end) != tc.end {
			t.Errorf("parseRange4(%q) = %s, %s, %v", tc.key, ip4(start), ip4(end), ok)
		}
	}
}

// The hand-rolled parser must agree with netip on everything, not just the
// well-formed keys a dump holds.
func TestParseIPv4AgreesWithNetip(t *testing.T) {
	inputs := []string{
		"0.0.0.0", "255.255.255.255", "1.2.3.4", "01.2.3.4", "1.2.3.04", "1.2.3.0",
		"256.1.1.1", "1.2.3", "1.2.3.4.5", "1..3.4", ".1.2.3", "1.2.3.", "1.2.3.4 ",
		"1234.1.1.1", "1.2.3.-4", "+1.2.3.4", "1.2.3.4a", "", "::1", "1.2.3.4/24",
		"99999999999999999999.1.1.1",
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		var b strings.Builder
		for range rng.IntN(16) {
			b.WriteByte("0123456789.."[rng.IntN(12)])
		}
		inputs = append(inputs, b.String())
	}
	for _, in := range inputs {
		got, ok := parseIPv4([]byte(in))
		want, err := netip.ParseAddr(in)
		wantOK := err == nil && want.Is4()
		if ok != wantOK || ok && ip4(got) != want.String() {
			t.Errorf("parseIPv4(%q) = %s, %v; netip says %v, %v", in, ip4(got), ok, want, err)
		}
	}
}

func TestParsePrefix6(t *testing.T) {
	for _, tc := range []struct {
		key        string
		start, end string
	}{
		{"2001:db8::/32", "2001:db8::", "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff"},
		{"2001:db8:1:2::/64", "2001:db8:1:2::", "2001:db8:1:2:ffff:ffff:ffff:ffff"},
		{"2001:db8::1/128", "2001:db8::1", "2001:db8::1"},
		{"2001:db8::/48", "2001:db8::", "2001:db8:0:ffff:ffff:ffff:ffff:ffff"},
		{"2001:db8:1::/36", "2001:db8::", "2001:db8:fff:ffff:ffff:ffff:ffff:ffff"}, // host bits cleared
		{"::/0", "::", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
		{"192.0.2.0/24", "", ""},
		{"::ffff:192.0.2.0/120", "", ""},
		{"2001:db8::", "", ""},
	} {
		start, end, ok := parsePrefix6([]byte(tc.key))
		if tc.start == "" {
			if ok {
				t.Errorf("parsePrefix6(%q) accepted", tc.key)
			}
			continue
		}
		if !ok || ip6(start) != tc.start || ip6(end) != tc.end {
			t.Errorf("parsePrefix6(%q) = %s, %s, %v", tc.key, ip6(start), ip6(end), ok)
		}
	}
}

// Whatever the blocks — nested, overlapping, adjacent, reaching the very end
// of the address space — the index answers every address with the country of
// the block over it that starts last, the narrowest among those starting
// together. That is the innermost block wherever blocks nest, which in RIPE's
// hierarchy is everywhere.
func TestIndexAnswersWithTheInnermostBlock(t *testing.T) {
	countries := [][2]byte{{}, {'D', 'E'}, {'N', 'L'}, {'S', 'E'}}
	regions4 := []v4addr{
		0,                    // the start of the space
		0x0a000000,           // anywhere
		math.MaxUint32 - 255, // the last block ends where the space does
	}
	regions6 := []v6addr{
		{0x20010db8_00000000, math.MaxUint64 - 127}, // across the low half's overflow
		{math.MaxUint64, math.MaxUint64 - 255},
	}

	for trial := range 300 {
		rng := rand.New(rand.NewPCG(uint64(trial), 7))
		var data ripeIndexData

		addrs4 := consecutive(regions4[trial%len(regions4)], 256)
		blocks4 := randomBlocks(rng, addrs4, countries)
		data.starts4, data.cc4 = flatten(slices.Clone(blocks4), compareCC)

		addrs6 := consecutive(regions6[trial%len(regions6)], 256)
		blocks6 := randomBlocks(rng, addrs6, countries)
		data.starts6, data.cc6 = flatten(slices.Clone(blocks6), compareCC)

		path := filepath.Join(t.TempDir(), "ripe.idx")
		if err := data.write(path); err != nil {
			t.Fatal(err)
		}
		idx, err := openRIPEIndex(path)
		if err != nil {
			t.Fatal(err)
		}
		err = expect(idx, addrs4, blocks4, ip4)
		if err == nil {
			err = expect(idx, addrs6, blocks6, ip6)
		}
		idx.close()
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}

// consecutive is n addresses from base, stopping at the end of the space.
func consecutive[A rangeAddr[A]](base A, n int) []A {
	addrs := []A{base}
	for len(addrs) < n {
		next, ok := addrs[len(addrs)-1].next()
		if !ok {
			break
		}
		addrs = append(addrs, next)
	}
	return addrs
}

// randomBlocks draws blocks over addrs, no two with the same range: RIPE
// cannot hold two, the range being the key.
func randomBlocks[A rangeAddr[A]](rng *rand.Rand, addrs []A, countries [][2]byte) []ripeBlock[A] {
	seen := map[[2]int]bool{}
	var blocks []ripeBlock[A]
	for range 1 + rng.IntN(24) {
		i, j := rng.IntN(len(addrs)), rng.IntN(len(addrs))
		if rng.IntN(4) == 0 {
			j = len(addrs) - 1 // often to the last address, to reach the end of the space
		}
		if i > j {
			i, j = j, i
		}
		if seen[[2]int{i, j}] {
			continue
		}
		seen[[2]int{i, j}] = true
		blocks = append(blocks, ripeBlock[A]{addrs[i], addrs[j], countries[rng.IntN(len(countries))]})
	}
	return blocks
}

// expect checks the index against the blocks, address by address.
func expect[A rangeAddr[A]](idx *ripeIndex, addrs []A, blocks []ripeBlock[A], str func(A) string) error {
	for _, a := range addrs {
		want := innermost(blocks, a)
		got := idx.lookup(net.ParseIP(str(a))).RegisteredCountryCode
		if got != strings.TrimRight(string(want[:]), "\x00") {
			return fmt.Errorf("%s: got %q, want %q from %v", str(a), got, want, blocks)
		}
	}
	return nil
}

func innermost[A rangeAddr[A]](blocks []ripeBlock[A], at A) [2]byte {
	var best *ripeBlock[A]
	for i := range blocks {
		b := &blocks[i]
		if at.less(b.start) || b.end.less(at) {
			continue
		}
		if best == nil || best.start.less(b.start) || best.start == b.start && b.end.less(best.end) {
			best = b
		}
	}
	if best == nil {
		return [2]byte{}
	}
	return best.val
}

// Neighbours with the same country become one range, and space no block
// covers is left out: that is where the saving over the raw dump comes from.
func TestFlattenMergesAndSkips(t *testing.T) {
	de, nl := [2]byte{'D', 'E'}, [2]byte{'N', 'L'}
	blocks := []ripeBlock[v4addr]{
		{100, 199, de},
		{200, 299, de}, // adjacent, same country: continues the range
		{150, 159, de}, // nested, same country: nothing changes
		{400, 499, nl}, // after a gap
	}
	starts, ccs := flatten(blocks, compareCC)
	got := fmt.Sprint(starts, ccs)
	want := fmt.Sprint([]v4addr{100, 300, 400, 500}, [][2]byte{de, {}, nl, {}})
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// ripeDumps serves the two dumps as RIPE does: gzipped, with Last-Modified,
// answering HEAD without the body.
type ripeDumps struct {
	mu         sync.Mutex
	v4, v6     []byte
	mod4, mod6 time.Time
	gets       int
}

func (d *ripeDumps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	body, mod := d.v4, d.mod4
	if r.URL.Path == "/inet6num.gz" {
		body, mod = d.v6, d.mod6
	}
	if r.Method == http.MethodGet {
		d.gets++
	}
	http.ServeContent(w, r, "", mod, bytes.NewReader(body))
}

func (d *ripeDumps) set(f func(d *ripeDumps)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f(d)
}

func (d *ripeDumps) getCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.gets
}

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const (
	testInetnum = `inetnum: 192.0.2.0 - 192.0.2.255
country: NL

inetnum: 192.0.2.128 - 192.0.2.255
country: DE
geofeed: https://geo.example/feed.csv

inetnum: 198.51.100.0 - 198.51.100.255
country: EU
remarks: Geofeed http://plain.example/feed.csv
`
	testInet6num = `inet6num: 2001:db8::/32
country: SE
remarks: Geofeed https://geo.example/v6.csv
`
)

func newTestRIPE(t *testing.T, d *ripeDumps) *RIPE {
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	p := NewRIPE(t.TempDir(), srv.Client())
	p.url4, p.url6 = srv.URL+"/inetnum.gz", srv.URL+"/inet6num.gz"
	p.min4, p.min6 = 3, 1
	p.reserve4, p.reserve6 = 0, 0
	t.Cleanup(p.Close)
	return p
}

func registered(p *RIPE, ip string) string {
	return p.Lookup(net.ParseIP(ip)).RegisteredCountryCode
}

func TestRIPEBuildsTheIndexAndRebuildsOnlyWhenADumpMoves(t *testing.T) {
	day := time.Date(2026, 9, 22, 22, 19, 0, 0, time.UTC)
	d := &ripeDumps{
		v4: gzipped(t, testInetnum), mod4: day,
		v6: gzipped(t, testInet6num), mod6: day,
	}
	p := newTestRIPE(t, d)

	if err := p.Download(); err != nil {
		t.Fatal(err)
	}
	if err := p.Open(); err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]string{
		"192.0.2.1":    "NL",
		"192.0.2.200":  "DE", // the nested block, not its parent
		"198.51.100.1": "",   // "EU" is not a country
		"203.0.113.1":  "",   // in no block
		"2001:db8::1":  "SE",
		"2001:db9::1":  "",
	} {
		if got := registered(p, ip); got != want {
			t.Errorf("%s: got %q, want %q", ip, got, want)
		}
	}
	if rec := p.Lookup(net.ParseIP("192.0.2.1")); rec.CountryCode != "" || !rec.HasData {
		t.Errorf("registration reported as location, or not at all: %+v", rec)
	}
	if n := d.getCount(); n != 2 {
		t.Fatalf("%d dump downloads for the first build, want 2", n)
	}
	// The geofeed links are listed beside the index, HTTPS ones only.
	links, err := os.ReadFile(p.linksPath())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(strings.Split(strings.TrimSpace(string(links)), "\n")[1:], "\n"),
		"192.0.2.128\t192.0.2.255\thttps://geo.example/feed.csv\n"+
			"2001:db8::\t2001:db8:ffff:ffff:ffff:ffff:ffff:ffff\thttps://geo.example/v6.csv"; got != want {
		t.Errorf("links:\n%s\nwant:\n%s", got, want)
	}

	// Unchanged: two HEADs, no download.
	if err := p.Download(); err != errNotModified {
		t.Fatalf("second check: %v, want errNotModified", err)
	}
	if n := d.getCount(); n != 2 {
		t.Errorf("unchanged dumps downloaded again: %d downloads", n)
	}

	// One dump moves: the index is rebuilt from both.
	d.set(func(d *ripeDumps) {
		d.v6 = gzipped(t, strings.Replace(testInet6num, "SE", "FI", 1))
		d.mod6 = day.Add(24 * time.Hour)
	})
	if err := p.Download(); err != nil {
		t.Fatal(err)
	}
	if err := p.Open(); err != nil {
		t.Fatal(err)
	}
	if got := registered(p, "2001:db8::1"); got != "FI" {
		t.Errorf("after the IPv6 dump moved: %q, want FI", got)
	}
	if got := registered(p, "192.0.2.1"); got != "NL" {
		t.Errorf("after the IPv6 dump moved, IPv4: %q, want NL", got)
	}
	if n := d.getCount(); n != 4 {
		t.Errorf("%d downloads after a rebuild, want 4", n)
	}
	if err := p.Download(); err != errNotModified {
		t.Errorf("check after the rebuild: %v, want errNotModified", err)
	}

	// An index without its links — one built before they were listed — is
	// incomplete, and rebuilt even though the dumps have not moved.
	os.Remove(p.linksPath())
	if p.FilesPresent() {
		t.Error("files present without the links")
	}
	if err := p.Download(); err != nil {
		t.Fatalf("Download without the links: %v, want a rebuild", err)
	}
	if !p.FilesPresent() {
		t.Error("the rebuild did not list the links")
	}
}

// A dump cut short, or one that parses to next to nothing, installs nothing:
// the index already there keeps answering.
func TestRIPEKeepsTheIndexWhenADumpIsBad(t *testing.T) {
	day := time.Date(2026, 9, 22, 22, 19, 0, 0, time.UTC)
	d := &ripeDumps{
		v4: gzipped(t, testInetnum), mod4: day,
		v6: gzipped(t, testInet6num), mod6: day,
	}
	p := newTestRIPE(t, d)
	if err := p.Download(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p.path())
	if err != nil {
		t.Fatal(err)
	}

	whole := gzipped(t, testInetnum+strings.Repeat("\ninetnum: 203.0.113.0 - 203.0.113.255\ncountry: FR\n", 200))
	for name, bad := range map[string]func(d *ripeDumps){
		"truncated": func(d *ripeDumps) { d.v4 = whole[:len(whole)/2] },
		"too few blocks": func(d *ripeDumps) {
			d.v4 = gzipped(t, "inetnum: 192.0.2.0 - 192.0.2.255\ncountry: NL\n")
		},
		"not gzip": func(d *ripeDumps) { d.v4 = []byte(testInetnum) },
	} {
		day = day.Add(24 * time.Hour)
		d.set(func(d *ripeDumps) { bad(d); d.mod4 = day })
		if err := p.Download(); err == nil || err == errNotModified {
			t.Errorf("%s: Download() = %v, want a failure", name, err)
		}
		after, err := os.ReadFile(p.path())
		if err != nil || !bytes.Equal(before, after) {
			t.Errorf("%s: installed index changed or went missing (%v)", name, err)
		}
		if _, err := os.Stat(p.path() + ".tmp"); !os.IsNotExist(err) {
			t.Errorf("%s: temp file left behind", name)
		}
	}
}

// An index whose stamps say it is current but which will not open is rebuilt,
// not kept until the next dump is published.
func TestRIPERebuildsADamagedIndex(t *testing.T) {
	day := time.Date(2026, 9, 22, 22, 19, 0, 0, time.UTC)
	d := &ripeDumps{
		v4: gzipped(t, testInetnum), mod4: day,
		v6: gzipped(t, testInet6num), mod6: day,
	}
	p := newTestRIPE(t, d)
	if err := p.Download(); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(p.path())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.path(), good[:len(good)-3], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Download(); err != nil {
		t.Fatalf("Download over a damaged index: %v, want a rebuild", err)
	}
	if err := p.Open(); err != nil {
		t.Fatalf("rebuilt index: %v", err)
	}
}

func TestRIPEIndexRefusesDamage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ripe.idx")
	data := ripeIndexData{
		starts4: []v4addr{10}, cc4: [][2]byte{{'D', 'E'}},
		starts6: []v6addr{{1, 0}}, cc6: [][2]byte{{'N', 'L'}},
	}
	if err := data.write(path); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRIPEIndex(good); err != nil {
		t.Fatalf("a good index refused: %v", err)
	}
	for name, bad := range map[string][]byte{
		"empty":       nil,
		"wrong magic": append([]byte("BRIPEIX0"), good[8:]...),
		"truncated":   good[:len(good)-1],
		"trailing":    append(bytes.Clone(good), 0),
	} {
		if _, err := parseRIPEIndex(bad); err == nil {
			t.Errorf("%s index accepted", name)
		}
	}
}

func ip4(a v4addr) string {
	return netip.AddrFrom4([4]byte{byte(a >> 24), byte(a >> 16), byte(a >> 8), byte(a)}).String()
}

func ip6(a v6addr) string {
	var b [16]byte
	be.PutUint64(b[:8], a.hi)
	be.PutUint64(b[8:], a.lo)
	return netip.AddrFrom16(b).String()
}
