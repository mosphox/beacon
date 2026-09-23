package geoip

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	geofeedLinksFile  = "ripe-geofeed-links.tsv"
	geofeedsIndexFile = "geofeeds.idx"
	geofeedsCacheDir  = "geofeeds"
	geofeedsStateFile = "state.json"

	// RFC 9632: without an Expires or max-age from its publisher, a feed is
	// fetched no more often than weekly; RFC 8805 asks for no less often.
	geofeedRefresh = 7 * 24 * time.Hour
	// A publisher's own expiry is honoured, but beacon looks at most twice a
	// day however short it is.
	geofeedMinRefresh = 12 * time.Hour
	// A feed that failed is tried again a day later. Its last good copy
	// stays in use for a month, then is dropped as too old to trust.
	geofeedRetry  = 24 * time.Hour
	geofeedMaxAge = 30 * 24 * time.Hour

	geofeedWorkers   = 16
	geofeedPerHost   = 2
	geofeedTimeout   = 45 * time.Second // one feed, the whole request
	geofeedPassLimit = 30 * time.Minute // one pass; what is left waits for the next
	maxGeofeedBytes  = 32 << 20
	maxGeofeedURL    = 1024
	maxGeofeedCity   = 128

	geofeedUserAgent = "beacon-geofeeds/1 (+https://github.com/mosphox/beacon)"

	geofeedsIndexMagic    = "BGEOFIX1"
	geofeedsIndexHeader   = 8 + sha256.Size + 4 + 4 + 4 // magic, inputs, three counts
	maxGeofeedsIndexBytes = 256 << 20

	// Changing how feeds are read changes the index they build, so it is part
	// of what the installed index is checked against.
	geofeedsFormat = "geofeeds/1"
)

// Geofeeds reports where network operators say their addresses are: the
// geofeed files (RFC 8805) they publish and link from their blocks in the
// RIPE Database (RFC 9632), usually down to the city. It is the operator's
// word, which the commercial databases fold into their own, here first-hand.
//
// The links come from the RIPE source's daily reading of the database, so
// this source runs only beside it. Each feed is kept on disk and fetched on
// its own schedule — weekly, or when its publisher's Expires or max-age says
// — with a conditional request, so a check costs its publisher a 304.
//
// A feed speaks only for the addresses it is linked from, and only where no
// more specific block links a feed of its own: RFC 9632's rules, without which
// any operator could publish a location for anyone's addresses.
type Geofeeds struct {
	dataDir string
	client  *http.Client

	dbs swappable
}

func NewGeofeeds(dataDir string) *Geofeeds { return newGeofeeds(dataDir, publicAddr, nil) }

// newGeofeeds takes the address check and the roots to trust, which tests
// replace to reach a local server.
func newGeofeeds(dataDir string, allow func(netip.Addr) bool, roots *x509.CertPool) *Geofeeds {
	return &Geofeeds{dataDir: dataDir, client: geofeedClient(allow, roots)}
}

func (p *Geofeeds) Name() string { return "Geofeeds" }

// Provides is what the format carries: a country, an ISO 3166-2 region and a
// city. Region and country come as codes; the page names them.
func (p *Geofeeds) Provides() Fields { return FieldCity | FieldRegion | FieldCountry }

func (p *Geofeeds) path() string { return filepath.Join(p.dataDir, geofeedsIndexFile) }

func (p *Geofeeds) cacheDir() string { return filepath.Join(p.dataDir, geofeedsCacheDir) }

// cachePath is where a feed's last good copy is kept.
func (p *Geofeeds) cachePath(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return filepath.Join(p.cacheDir(), hex.EncodeToString(sum[:16])+".csv")
}

func (p *Geofeeds) FilesPresent() bool { return filesPresent(p.path()) }

func (p *Geofeeds) Lookup(ip net.IP) Record { return p.dbs.lookup(ip) }

func (p *Geofeeds) Open() error {
	idx, err := openGeofeedsIndex(p.path())
	if err != nil {
		return err
	}
	p.dbs.swap(idx)
	return nil
}

func (p *Geofeeds) Close() { p.dbs.closeAll() }

// A check fetches only the feeds that are due and rebuilds only when
// something moved: RIPE's links, which change daily, or a feed.
func (p *Geofeeds) MinRefresh() time.Duration { return 0 }

func (p *Geofeeds) Download() error {
	dest := p.path()
	removeStaleTemps(dest)

	links, linksRaw, err := readGeofeedLinks(geofeedLinksPath(p.dataDir))
	if err != nil {
		return fmt.Errorf("Geofeeds: %w", err)
	}
	if err := os.MkdirAll(p.cacheDir(), 0o755); err != nil {
		return fmt.Errorf("Geofeeds: %w", err)
	}
	urls := linkedFeeds(links)
	state := p.loadState()
	now := time.Now()
	pass := p.fetchDue(urls, state, now)
	p.forget(state, urls)
	if err := p.saveState(state); err != nil {
		log.Printf("Geofeeds: save feed state: %v", err)
	}
	if pass.fetched+pass.unchanged+pass.failed+pass.deferred > 0 {
		log.Printf("Geofeeds: %d feeds linked; checked %d: %d new or changed, %d unchanged, %d failed, %d left for the next pass",
			len(urls), pass.fetched+pass.unchanged+pass.failed, pass.fetched, pass.unchanged, pass.failed, pass.deferred)
	}

	now = time.Now()
	usable := func(u string) bool {
		st := state[u]
		return st != nil && st.SHA256 != "" && now.Sub(st.Checked) <= geofeedMaxAge && filesPresent(p.cachePath(u))
	}
	fp := geofeedsFingerprint(linksRaw, urls, state, usable)
	if idx, err := openGeofeedsIndex(dest); err == nil {
		same := idx.inputs == fp
		idx.close()
		if same {
			return errNotModified
		}
	}

	b := newGeofeedBuild(links, urls)
	for i, u := range urls {
		if !usable(u) {
			continue
		}
		body, err := os.ReadFile(p.cachePath(u))
		if err != nil {
			continue
		}
		b.addFeed(uint32(i+1), body)
	}
	if b.feeds == 0 {
		return errors.New("Geofeeds: no linked feed could be fetched")
	}

	data := b.index(fp)
	tmp := dest + ".tmp"
	if err := data.write(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("Geofeeds: write index: %w", err)
	}
	check, err := openGeofeedsIndex(tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("Geofeeds: built index unreadable: %w", err)
	}
	check.close()
	if err := install([]pendingFile{{tmp: tmp, dest: dest}}); err != nil {
		return err
	}
	log.Printf("Geofeeds: indexed %d feeds: %d entries used, %d outside the blocks that link them, %d malformed, %d repeated; %d IPv4 and %d IPv6 ranges, %d places",
		b.feeds, b.used, b.outside, b.malformed, b.repeated, len(data.starts4), len(data.starts6), len(data.places))
	return nil
}

// ------------------------------------------------------------------ links

// geofeedLinksPath is where the RIPE source lists the blocks that link a
// geofeed, one per line: first address, last address, URL.
func geofeedLinksPath(dataDir string) string { return filepath.Join(dataDir, geofeedLinksFile) }

type linksWriter struct {
	buf bytes.Buffer
	n   int
}

func newLinksWriter() *linksWriter {
	w := &linksWriter{}
	w.buf.WriteString("# Blocks in the RIPE Database that link a geofeed: first address, last address, URL.\n")
	return w
}

// add records a block's geofeed link, if it has one that can be followed.
func (w *linksWriter) add(first, last netip.Addr, rawURL []byte) {
	if len(rawURL) == 0 || !validGeofeedURL(string(rawURL)) {
		return
	}
	w.buf.WriteString(first.String())
	w.buf.WriteByte('\t')
	w.buf.WriteString(last.String())
	w.buf.WriteByte('\t')
	w.buf.Write(rawURL)
	w.buf.WriteByte('\n')
	w.n++
}

func (w *linksWriter) bytes() []byte { return w.buf.Bytes() }

// validGeofeedURL accepts what RFC 9632 allows a link to be: HTTPS, and
// nothing else.
func validGeofeedURL(raw string) bool {
	if len(raw) > maxGeofeedURL {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= ' ' || raw[i] == 0x7f {
			return false
		}
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

type geofeedLink struct {
	first, last netip.Addr
	url         string
}

// readGeofeedLinks reads the RIPE source's list, and returns it raw too: what
// the list says is part of what the index was built from.
func readGeofeedLinks(path string) ([]geofeedLink, []byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, errors.New("no geofeed links yet: the RIPE source lists them when it builds its index")
	}
	if err != nil {
		return nil, nil, err
	}
	var links []geofeedLink
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		f := bytes.Split(line, []byte("\t"))
		if len(f) != 3 {
			continue
		}
		first, err1 := netip.ParseAddr(string(f[0]))
		last, err2 := netip.ParseAddr(string(f[1]))
		u := string(f[2])
		if err1 != nil || err2 != nil || first.Is4() != last.Is4() || last.Less(first) || !validGeofeedURL(u) {
			continue
		}
		links = append(links, geofeedLink{first, last, u})
	}
	return links, raw, nil
}

// linkedFeeds is every feed the links name, sorted, each once.
func linkedFeeds(links []geofeedLink) []string {
	urls := make([]string, 0, len(links))
	for _, l := range links {
		urls = append(urls, l.url)
	}
	slices.Sort(urls)
	return slices.Compact(urls)
}

// --------------------------------------------------------------- fetching

// feedState is what beacon remembers about a feed between passes.
type feedState struct {
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	Checked      time.Time `json:"checked,omitzero"` // last answered, 200 or 304
	Next         time.Time `json:"next"`
	SHA256       string    `json:"sha256,omitempty"` // of the copy on disk
	Failures     int       `json:"failures,omitempty"`
	Error        string    `json:"error,omitempty"`
}

func (p *Geofeeds) statePath() string { return filepath.Join(p.cacheDir(), geofeedsStateFile) }

func (p *Geofeeds) loadState() map[string]*feedState {
	state := map[string]*feedState{}
	data, err := os.ReadFile(p.statePath())
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("Geofeeds: feed state unreadable, starting afresh: %v", err)
		return map[string]*feedState{}
	}
	return state
}

func (p *Geofeeds) saveState(state map[string]*feedState) error {
	data, err := json.MarshalIndent(state, "", " ")
	if err != nil {
		return err
	}
	return writeFileAtomic(p.statePath(), data)
}

// forget drops the feeds no longer linked, and any file in the cache that
// belongs to none.
func (p *Geofeeds) forget(state map[string]*feedState, urls []string) {
	keep := map[string]bool{filepath.Base(p.statePath()): true}
	for _, u := range urls {
		keep[filepath.Base(p.cachePath(u))] = true
	}
	for u := range state {
		if !keep[filepath.Base(p.cachePath(u))] {
			delete(state, u)
		}
	}
	entries, err := os.ReadDir(p.cacheDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			os.Remove(filepath.Join(p.cacheDir(), e.Name()))
		}
	}
}

type fetchPass struct{ fetched, unchanged, failed, deferred int }

// fetchDue fetches every feed that is due, a few at a time and at most two
// from any one host at once, within an overall limit; feeds the limit cuts
// off wait for the next pass without counting as failures.
func (p *Geofeeds) fetchDue(urls []string, state map[string]*feedState, now time.Time) fetchPass {
	var due []string
	for _, u := range urls {
		if st := state[u]; st == nil || !now.Before(st.Next) {
			due = append(due, u)
		}
	}
	due = interleaveHosts(due)

	ctx, cancel := context.WithTimeout(context.Background(), geofeedPassLimit)
	defer cancel()
	hosts := &hostSlots{slots: map[string]chan struct{}{}}

	var (
		mu   sync.Mutex
		pass fetchPass
		wg   sync.WaitGroup
	)
	jobs := make(chan string)
	for range min(geofeedWorkers, len(due)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				mu.Lock()
				prev := state[u]
				mu.Unlock()
				next, outcome := p.fetchOne(ctx, u, prev, hosts)
				mu.Lock()
				if next != nil {
					state[u] = next
				}
				switch outcome {
				case feedFetched:
					pass.fetched++
				case feedUnchanged:
					pass.unchanged++
				case feedFailed:
					pass.failed++
				default:
					pass.deferred++
				}
				mu.Unlock()
			}
		}()
	}
	for i, u := range due {
		if ctx.Err() != nil {
			mu.Lock()
			pass.deferred += len(due) - i
			mu.Unlock()
			break
		}
		jobs <- u
	}
	close(jobs)
	wg.Wait()
	return pass
}

type feedOutcome int

const (
	feedDeferred feedOutcome = iota
	feedFetched
	feedUnchanged
	feedFailed
)

// fetchOne asks for one feed, conditionally when a copy is on disk. It
// returns the feed's new state, or nil to leave it as it was.
func (p *Geofeeds) fetchOne(ctx context.Context, rawURL string, prev *feedState, hosts *hostSlots) (*feedState, feedOutcome) {
	release, err := hosts.acquire(ctx, hostOf(rawURL))
	if err != nil {
		return nil, feedDeferred
	}
	defer release()

	st := &feedState{}
	if prev != nil {
		*st = *prev
	}
	fail := func(err error) (*feedState, feedOutcome) {
		st.Failures++
		st.Error = err.Error()
		st.Next = time.Now().Add(geofeedRetry)
		return st, feedFailed
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fail(sanitizeURLErr(err))
	}
	req.Header.Set("User-Agent", geofeedUserAgent)
	if st.SHA256 != "" && filesPresent(p.cachePath(rawURL)) {
		if st.ETag != "" {
			req.Header.Set("If-None-Match", st.ETag)
		}
		if st.LastModified != "" {
			req.Header.Set("If-Modified-Since", st.LastModified)
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, feedDeferred
		}
		return fail(sanitizeURLErr(err))
	}
	defer resp.Body.Close()
	now := time.Now()

	switch resp.StatusCode {
	case http.StatusNotModified:
		if st.SHA256 == "" {
			return fail(errors.New("not modified, but nothing is on disk"))
		}
		if etag := resp.Header.Get("ETag"); etag != "" {
			st.ETag = etag
		}
		st.Checked, st.Next, st.Failures, st.Error = now, nextFetch(resp.Header, now), 0, ""
		return st, feedUnchanged

	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxGeofeedBytes+1))
		if err != nil {
			if ctx.Err() != nil {
				return nil, feedDeferred
			}
			return fail(err)
		}
		if len(body) > maxGeofeedBytes {
			return fail(fmt.Errorf("larger than %d bytes", maxGeofeedBytes))
		}
		sum := sha256.Sum256(body)
		digest := hex.EncodeToString(sum[:])
		if digest != st.SHA256 || !filesPresent(p.cachePath(rawURL)) {
			if err := writeFileAtomic(p.cachePath(rawURL), body); err != nil {
				return fail(err)
			}
		}
		outcome := feedFetched
		if digest == st.SHA256 {
			outcome = feedUnchanged // sent in full, but the same as before
		}
		st.SHA256, st.ETag, st.LastModified = digest, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified")
		st.Checked, st.Next, st.Failures, st.Error = now, nextFetch(resp.Header, now), 0, ""
		return st, outcome

	default:
		return fail(fmt.Errorf("status %d", resp.StatusCode))
	}
}

// nextFetch is when a feed is due again: weekly, or sooner when its publisher
// says it expires sooner, but never within geofeedMinRefresh. max-age takes
// precedence over Expires, as HTTP caching has it.
func nextFetch(h http.Header, now time.Time) time.Time {
	next := now.Add(geofeedRefresh)
	if exp, ok := feedExpiry(h, now); ok && exp.Before(next) {
		next = exp
	}
	if floor := now.Add(geofeedMinRefresh); next.Before(floor) {
		next = floor
	}
	return next
}

func feedExpiry(h http.Header, now time.Time) (time.Time, bool) {
	for directive := range strings.SplitSeq(h.Get("Cache-Control"), ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(directive), "=")
		if !strings.EqualFold(name, "max-age") {
			continue
		}
		secs, err := strconv.ParseInt(strings.Trim(value, `"`), 10, 64)
		if err != nil || secs < 0 {
			break
		}
		return now.Add(time.Duration(min(secs, int64(geofeedRefresh/time.Second))) * time.Second), true
	}
	if exp, err := http.ParseTime(h.Get("Expires")); err == nil {
		return exp, true
	}
	return time.Time{}, false
}

// hostSlots limits how many requests go to one host at once.
type hostSlots struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

func (h *hostSlots) acquire(ctx context.Context, host string) (func(), error) {
	h.mu.Lock()
	slot, ok := h.slots[host]
	if !ok {
		slot = make(chan struct{}, geofeedPerHost)
		h.slots[host] = slot
	}
	h.mu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// interleaveHosts orders feeds so that consecutive ones come from different
// hosts where possible: a host with many feeds then waits its turn instead of
// holding every worker.
func interleaveHosts(urls []string) []string {
	byHost := map[string][]string{}
	var hosts []string
	for _, u := range urls {
		h := hostOf(u)
		if _, ok := byHost[h]; !ok {
			hosts = append(hosts, h)
		}
		byHost[h] = append(byHost[h], u)
	}
	out := make([]string, 0, len(urls))
	for len(out) < len(urls) {
		for _, h := range hosts {
			if q := byHost[h]; len(q) > 0 {
				out = append(out, q[0])
				byHost[h] = q[1:]
			}
		}
	}
	return out
}

// geofeedClient is how feeds are fetched: HTTPS only, and only from public
// addresses. The links come from records anyone can create, so a link to a
// private or loopback address — this host's own services, the Docker
// network — is refused, and the check runs at connect time on the address
// actually dialled, redirects and DNS rebinding included.
func geofeedClient(allow func(netip.Addr) bool, roots *x509.CertPool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return err
			}
			if !allow(ip) {
				return fmt.Errorf("refused to connect to %s: not a public address", ip)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   geofeedPerHost,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   geofeedTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("redirected away from HTTPS")
			}
			req.Header.Del("Referer")
			return nil
		},
	}
}

// nonPublic is address space that IsGlobalUnicast and IsPrivate let through
// but that is no public server's: shared, reserved and documentation ranges,
// and IPv6 ranges that embed an IPv4 address.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// geofeedsFingerprint identifies what an index is built from: the reading of
// the format, RIPE's links, and every usable feed's content.
func geofeedsFingerprint(linksRaw []byte, urls []string, state map[string]*feedState, usable func(string) bool) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte(geofeedsFormat + "\n"))
	links := sha256.Sum256(linksRaw)
	h.Write(links[:])
	for _, u := range urls {
		h.Write([]byte(u))
		h.Write([]byte{0})
		if usable(u) {
			h.Write([]byte(state[u].SHA256))
		}
		h.Write([]byte{'\n'})
	}
	var fp [sha256.Size]byte
	copy(fp[:], h.Sum(nil))
	return fp
}

// ---------------------------------------------------------------- parsing

// geoPlace is what a feed entry says: an ISO country code, the subdivision
// part of an ISO 3166-2 region code ("M" of "SE-M"), and a city.
type geoPlace struct {
	cc, region, city string
}

// parseGeofeed reads an RFC 8805 feed and calls fn with each entry that
// parses, returning how many did not. An entry whose location fields are all
// empty — or "ZZ", the older way to say so — is kept, with no place: it is
// the publisher saying the range has no location, which must still override
// a wider entry.
func parseGeofeed(body []byte, fn func(netip.Prefix, geoPlace)) (malformed int) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	for sc.Scan() {
		line := sc.Bytes()
		if i := bytes.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		fields := strings.Split(string(line), ",")
		field := func(i int) string {
			if i >= len(fields) {
				return ""
			}
			return strings.Trim(strings.TrimSpace(fields[i]), `"`)
		}
		pfx, ok := parseFeedPrefix(field(0))
		if !ok {
			malformed++
			continue
		}
		place, ok := parseFeedPlace(field(1), field(2), field(3))
		if !ok {
			malformed++
			continue
		}
		fn(pfx, place)
	}
	if sc.Err() != nil {
		malformed++ // a line too long to be an entry ends the feed there
	}
	return malformed
}

// parseFeedPrefix reads a prefix or a single address, either family, in any
// valid spelling, as RFC 8805 requires.
func parseFeedPrefix(s string) (netip.Prefix, bool) {
	var pfx netip.Prefix
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, false
		}
		pfx = p.Masked()
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, false
		}
		pfx = netip.PrefixFrom(a, a.BitLen())
	}
	if pfx.Addr().Is4In6() {
		return netip.Prefix{}, false
	}
	return pfx, true
}

func parseFeedPlace(cc, region, city string) (geoPlace, bool) {
	cc, region = strings.ToUpper(cc), strings.ToUpper(region)
	if cc == "ZZ" {
		return geoPlace{}, true
	}
	if cc != "" {
		if len(cc) != 2 || !isLetters(cc) {
			return geoPlace{}, false
		}
		cc = countryCode(cc) // "EU" and the like are no country
	}
	var sub string
	if region != "" {
		country, part, ok := strings.Cut(region, "-")
		if !ok || len(country) != 2 || !isLetters(country) || len(part) < 1 || len(part) > 3 || !isAlnum(part) {
			return geoPlace{}, false
		}
		if cc == "" {
			cc = countryCode(country)
		}
		if country != cc {
			return geoPlace{}, false
		}
		sub = part
	}
	if len(city) > maxGeofeedCity || !utf8.ValidString(city) || strings.ContainsFunc(city, unicode.IsControl) {
		return geoPlace{}, false
	}
	return geoPlace{cc: cc, region: sub, city: city}, true
}

func isLetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

func isAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		if (s[i] < 'A' || s[i] > 'Z') && (s[i] < '0' || s[i] > '9') {
			return false
		}
	}
	return true
}

// --------------------------------------------------------------- building

// geofeedBuild turns feeds into an index. Which feed may speak for which
// addresses is settled first, from RIPE's links: each address belongs to the
// feed linked by the most specific block over it that links one. Every entry
// is then cut down to the addresses its own feed speaks for.
type geofeedBuild struct {
	owner4 []v4addr
	feed4  []uint32
	owner6 []v6addr
	feed6  []uint32

	places  map[geoPlace]uint32
	ordered []geoPlace // place n is ordered[n-1]; 0 is no place
	spans4  []span[v4addr, uint32]
	spans6  []span[v6addr, uint32]

	feeds, used, outside, malformed, repeated int
}

func newGeofeedBuild(links []geofeedLink, urls []string) *geofeedBuild {
	ids := make(map[string]uint32, len(urls))
	for i, u := range urls {
		ids[u] = uint32(i + 1)
	}
	var claims4 []span[v4addr, uint32]
	var claims6 []span[v6addr, uint32]
	for _, l := range links {
		if l.first.Is4() {
			claims4 = append(claims4, span[v4addr, uint32]{v4of(l.first), v4of(l.last), ids[l.url]})
		} else {
			claims6 = append(claims6, span[v6addr, uint32]{v6of(l.first), v6of(l.last), ids[l.url]})
		}
	}
	b := &geofeedBuild{places: map[geoPlace]uint32{}}
	b.owner4, b.feed4 = flatten(claims4, cmp.Compare[uint32])
	b.owner6, b.feed6 = flatten(claims6, cmp.Compare[uint32])
	return b
}

func (b *geofeedBuild) place(pl geoPlace) uint32 {
	if pl == (geoPlace{}) {
		return 0
	}
	id, ok := b.places[pl]
	if !ok {
		b.ordered = append(b.ordered, pl)
		id = uint32(len(b.ordered))
		b.places[pl] = id
	}
	return id
}

// addFeed takes the entries of feed id that fall where it may speak. A prefix
// listed twice is an error in the feed (RFC 8805); the first listing stands.
func (b *geofeedBuild) addFeed(id uint32, body []byte) {
	b.feeds++
	seen := map[netip.Prefix]bool{}
	b.malformed += parseGeofeed(body, func(pfx netip.Prefix, pl geoPlace) {
		if seen[pfx] {
			b.repeated++
			return
		}
		seen[pfx] = true
		val := b.place(pl)
		kept := false
		if pfx.Addr().Is4() {
			first := v4of(pfx.Addr())
			last := first | v4addr(uint32(1<<(32-pfx.Bits())-1))
			clip(b.owner4, b.feed4, id, first, last, func(from, to v4addr) {
				b.spans4 = append(b.spans4, span[v4addr, uint32]{from, to, val})
				kept = true
			})
		} else {
			first := v6of(pfx.Addr())
			clip(b.owner6, b.feed6, id, first, first.lastIn(pfx.Bits()), func(from, to v6addr) {
				b.spans6 = append(b.spans6, span[v6addr, uint32]{from, to, val})
				kept = true
			})
		}
		if kept {
			b.used++
		} else {
			b.outside++
		}
	})
}

// clip calls emit with each part of [start, end] the partition gives to want.
func clip[A rangeAddr[A], V comparable](starts []A, vals []V, want V, start, end A, emit func(from, to A)) {
	i := sort.Search(len(starts), func(i int) bool { return start.less(starts[i]) }) - 1
	if i < 0 {
		i = 0 // before the first start, nothing is anyone's
	}
	for ; i < len(starts) && !end.less(starts[i]); i++ {
		if vals[i] != want {
			continue
		}
		from, to := start, end
		if from.less(starts[i]) {
			from = starts[i]
		}
		if i+1 < len(starts) {
			if last, _ := starts[i+1].prev(); last.less(to) {
				to = last
			}
		}
		emit(from, to)
	}
}

// index flattens the entries: where a feed nests prefixes, the most specific
// one answers, as RFC 8805 has it.
func (b *geofeedBuild) index(inputs [sha256.Size]byte) *geofeedsIndexData {
	d := &geofeedsIndexData{inputs: inputs, places: b.ordered}
	d.starts4, d.ids4 = flatten(b.spans4, cmp.Compare[uint32])
	d.starts6, d.ids6 = flatten(b.spans6, cmp.Compare[uint32])
	return d
}

// ------------------------------------------------------------------ index

// geofeedsIndexData is a built index, before it is written. On disk it is the
// magic, the fingerprint of its inputs and three counts, then for IPv4 the
// starts and their place numbers, the same for IPv6, then the places: an
// offset table and, for each, the country code (two bytes, zero for none),
// the region and the city, each after its length byte. Big-endian throughout.
type geofeedsIndexData struct {
	inputs  [sha256.Size]byte
	starts4 []v4addr
	ids4    []uint32
	starts6 []v6addr
	ids6    []uint32
	places  []geoPlace
}

func (d *geofeedsIndexData) write(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)

	var hdr [geofeedsIndexHeader]byte
	copy(hdr[:], geofeedsIndexMagic)
	copy(hdr[8:], d.inputs[:])
	be.PutUint32(hdr[8+sha256.Size:], uint32(len(d.starts4)))
	be.PutUint32(hdr[12+sha256.Size:], uint32(len(d.starts6)))
	be.PutUint32(hdr[16+sha256.Size:], uint32(len(d.places)))
	w.Write(hdr[:])

	var b [16]byte
	for _, s := range d.starts4 {
		be.PutUint32(b[:4], uint32(s))
		w.Write(b[:4])
	}
	for _, id := range d.ids4 {
		be.PutUint32(b[:4], id)
		w.Write(b[:4])
	}
	for _, s := range d.starts6 {
		be.PutUint64(b[:8], s.hi)
		be.PutUint64(b[8:], s.lo)
		w.Write(b[:])
	}
	for _, id := range d.ids6 {
		be.PutUint32(b[:4], id)
		w.Write(b[:4])
	}

	var blob []byte
	offsets := make([]uint32, 0, len(d.places)+1)
	for _, pl := range d.places {
		offsets = append(offsets, uint32(len(blob)))
		cc := [2]byte{}
		copy(cc[:], pl.cc)
		blob = append(blob, cc[:]...)
		blob = append(blob, byte(len(pl.region)))
		blob = append(blob, pl.region...)
		blob = append(blob, byte(len(pl.city)))
		blob = append(blob, pl.city...)
	}
	offsets = append(offsets, uint32(len(blob)))
	for _, o := range offsets {
		be.PutUint32(b[:4], o)
		w.Write(b[:4])
	}
	w.Write(blob)

	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// geofeedsIndex is an opened index: views into a memory-mapped file, read
// only under swappable's lock, with whatever a lookup returns copied out.
type geofeedsIndex struct {
	release func() error

	inputs        [sha256.Size]byte
	n4, n6, np    int
	starts4, ids4 []byte
	starts6, ids6 []byte
	offsets, blob []byte
}

func openGeofeedsIndex(path string) (*geofeedsIndex, error) {
	data, release, err := mapFile(path, maxGeofeedsIndexBytes)
	if err != nil {
		return nil, err
	}
	idx, err := parseGeofeedsIndex(data)
	if err != nil {
		release()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	idx.release = release
	return idx, nil
}

func parseGeofeedsIndex(data []byte) (*geofeedsIndex, error) {
	if len(data) < geofeedsIndexHeader || string(data[:len(geofeedsIndexMagic)]) != geofeedsIndexMagic {
		return nil, errors.New("not a geofeeds index")
	}
	idx := &geofeedsIndex{
		n4: int(be.Uint32(data[8+sha256.Size:])),
		n6: int(be.Uint32(data[12+sha256.Size:])),
		np: int(be.Uint32(data[16+sha256.Size:])),
	}
	copy(idx.inputs[:], data[8:])
	fixed := uint64(geofeedsIndexHeader) + uint64(idx.n4)*8 + uint64(idx.n6)*20 + uint64(idx.np+1)*4
	if uint64(len(data)) < fixed {
		return nil, fmt.Errorf("geofeeds index is %d bytes, its counts need %d", len(data), fixed)
	}
	off := geofeedsIndexHeader
	take := func(n int) []byte { s := data[off : off+n]; off += n; return s }
	idx.starts4 = take(idx.n4 * 4)
	idx.ids4 = take(idx.n4 * 4)
	idx.starts6 = take(idx.n6 * 16)
	idx.ids6 = take(idx.n6 * 4)
	idx.offsets = take((idx.np + 1) * 4)
	idx.blob = data[off:]

	// Every place must lie inside the blob, in order, and every number in
	// the tables must name one: a lookup then never reads out of bounds.
	prev := uint32(0)
	for i := 0; i <= idx.np; i++ {
		o := be.Uint32(idx.offsets[i*4:])
		if o < prev || int(o) > len(idx.blob) {
			return nil, errors.New("geofeeds index: place table out of order")
		}
		prev = o
	}
	if int(prev) != len(idx.blob) {
		return nil, errors.New("geofeeds index: trailing bytes after the places")
	}
	for _, ids := range [][]byte{idx.ids4, idx.ids6} {
		for i := 0; i < len(ids); i += 4 {
			if int(be.Uint32(ids[i:])) > idx.np {
				return nil, errors.New("geofeeds index: a range names a place that is not there")
			}
		}
	}
	return idx, nil
}

func (x *geofeedsIndex) lookup(ip net.IP) Record {
	var rec Record
	if x == nil {
		return rec
	}
	var id uint32
	if v4 := ip.To4(); v4 != nil {
		a := be.Uint32(v4)
		i := sort.Search(x.n4, func(i int) bool { return be.Uint32(x.starts4[i*4:]) > a }) - 1
		if i < 0 {
			return rec
		}
		id = be.Uint32(x.ids4[i*4:])
	} else if v6 := ip.To16(); v6 != nil {
		hi, lo := be.Uint64(v6[:8]), be.Uint64(v6[8:])
		i := sort.Search(x.n6, func(i int) bool {
			s := x.starts6[i*16:]
			shi, slo := be.Uint64(s), be.Uint64(s[8:])
			return shi > hi || shi == hi && slo > lo
		}) - 1
		if i < 0 {
			return rec
		}
		id = be.Uint32(x.ids6[i*4:])
	}
	if id == 0 {
		return rec
	}
	pl := x.blob[be.Uint32(x.offsets[(id-1)*4:]):be.Uint32(x.offsets[id*4:])]
	if len(pl) < 4 {
		return rec
	}
	if pl[0] != 0 {
		rec.CountryCode = string(pl[:2])
	}
	rl := int(pl[2])
	if 3+rl >= len(pl) {
		return rec
	}
	if rl > 0 {
		rec.Subdivisions = []Subdivision{{Code: string(pl[3 : 3+rl])}}
	}
	cl := int(pl[3+rl])
	if 4+rl+cl > len(pl) {
		return Record{}
	}
	rec.City = string(pl[4+rl : 4+rl+cl])
	rec.HasData = rec.CountryCode != "" || rl > 0 || cl > 0
	return rec
}

func (x *geofeedsIndex) close() {
	if x != nil && x.release != nil {
		x.release()
		x.release = nil
	}
}
