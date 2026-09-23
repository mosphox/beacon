package geoip

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	downloadTimeout  = 5 * time.Minute
	fallbackInterval = 12 * time.Hour

	// checkInterval is how often the refresh loop looks over every source, to
	// bring up one that is not answering yet and refresh one that is due. Due
	// is each source's own clock — its stamp against its interval — so looking
	// often costs a few small file reads and holds each source to its interval.
	// Waking once per interval instead, a source stamped just after one wake-up
	// was a moment short of due at the next, and waited out two intervals.
	checkInterval = 15 * time.Minute
)

// Provider is one source of GeoIP data. Each owns its own files in the data
// directory and its own download protocol.
type Provider interface {
	Name() string
	// Provides is what the source can fill for any address, as opposed to
	// what it happens to know about one.
	Provides() Fields
	FilesPresent() bool
	// Download fetches and installs the latest data, or returns errNotModified
	// when the installed data is already the latest.
	Download() error
	Open() error
	Close()
	Lookup(ip net.IP) Record
	MinRefresh() time.Duration
}

// Registry holds every enabled provider and answers lookups from all of them.
type Registry struct {
	dataDir  string
	interval time.Duration
	check    time.Duration
	rank     map[Provider]int // priority: position in the list given to NewRegistry

	// held is the wait imposed on each source that failed, touched only by
	// NewRegistry and then by the refresh loop.
	held map[Provider]holdoff

	mu        sync.RWMutex
	providers []Provider // answering lookups, in priority order
	pending   []Provider // not answering yet; brought up by the refresh loop
}

func NewRegistry(dataDir string, interval time.Duration, providers ...Provider) (*Registry, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no GeoIP providers enabled")
	}
	// providers is filled by the loops below; seeding it here would double every entry.
	r := &Registry{
		dataDir:  dataDir,
		interval: interval,
		check:    checkInterval,
		rank:     make(map[Provider]int, len(providers)),
		held:     make(map[Provider]holdoff),
	}
	for i, p := range providers {
		r.rank[p] = i
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// Sources whose data is already on disk come up now: they only need
	// opening. One that still has to download does it in the background, from
	// the refresh loop, and starts answering when it is done. A first download
	// can take a minute — RIPE's dumps are a quarter of a gigabyte — and the
	// service should not be down for it while other sources could answer. Only
	// when none can is there nothing to serve without waiting.
	//
	// A source that cannot be brought up is set aside rather than being fatal,
	// so one failing source never takes the service down when another works.
	var missing []Provider
	for _, p := range providers {
		if !p.FilesPresent() {
			missing = append(missing, p)
			continue
		}
		r.start(p)
	}
	for _, p := range missing {
		if len(r.providers) > 0 {
			log.Printf("%s: databases missing, downloading in the background", p.Name())
			r.pending = append(r.pending, p)
			continue
		}
		r.start(p)
	}
	if len(r.providers) == 0 {
		return nil, fmt.Errorf("no GeoIP provider could be initialised")
	}
	r.sortByRank()
	return r, nil
}

// start brings p up now, or sets it aside for the refresh loop to retry.
func (r *Registry) start(p Provider) {
	if err := r.bring(p); err != nil {
		log.Printf("%s: unavailable, next try in %s: %v", p.Name(), r.failed(p), err)
		r.pending = append(r.pending, p)
		return
	}
	log.Printf("%s: ready", p.Name())
	r.providers = append(r.providers, p)
}

// sortByRank restores priority order, which the top-level answer depends on,
// after a source joins late. Callers hold the write lock, or own r outright.
func (r *Registry) sortByRank() {
	sort.SliceStable(r.providers, func(i, j int) bool {
		return r.rank[r.providers[i]] < r.rank[r.providers[j]]
	})
}

// live returns a snapshot of the answering providers.
func (r *Registry) live() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Provider(nil), r.providers...)
}

// promote moves a provider from pending to answering, in its priority place.
func (r *Registry) promote(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, q := range r.pending {
		if q == p {
			r.pending = append(r.pending[:i], r.pending[i+1:]...)
			break
		}
	}
	r.providers = append(r.providers, p)
	r.sortByRank()
}

// bring makes p ready to answer. What is on disk is opened; when nothing is,
// or what is there will not open, it downloads first — so a damaged file is
// replaced instead of keeping the source down until someone deletes it.
func (r *Registry) bring(p Provider) error {
	if p.FilesPresent() {
		err := p.Open()
		if err == nil {
			return nil
		}
		log.Printf("%s: installed databases unusable, downloading again: %v", p.Name(), err)
	} else {
		log.Printf("%s: databases missing, downloading...", p.Name())
	}
	if err := p.Download(); err != nil {
		return err
	}
	if err := p.Open(); err != nil {
		return err
	}
	// Stamp only once the files have proven openable, so unusable data is
	// retried rather than treated as fresh.
	r.stamp(p)
	return nil
}

// Sources lists the providers currently answering, in priority order.
func (r *Registry) Sources() []string {
	live := r.live()
	out := make([]string, 0, len(live))
	for _, p := range live {
		out = append(out, p.Name())
	}
	return out
}

// LookupAll returns one answer per provider that had data for the address.
func (r *Registry) LookupAll(ipStr string) []Answer {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil
	}
	live := r.live()
	answers := make([]Answer, 0, len(live))
	for _, p := range live {
		rec := p.Lookup(ip)
		if !rec.HasData {
			continue
		}
		answers = append(answers, Answer{Source: p.Name(), Record: rec, Provides: p.Provides()})
	}
	return answers
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.providers {
		p.Close()
	}
	r.providers = nil
}

// stampPath records when a provider last installed fresh data, or last
// confirmed that what it has installed is still the latest.
func (r *Registry) stampPath(p Provider) string {
	name := strings.ToLower(strings.ReplaceAll(p.Name(), "-", ""))
	return filepath.Join(r.dataDir, "."+name+".timestamp")
}

func (r *Registry) stamp(p Provider) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	if err := os.WriteFile(r.stampPath(p), []byte(ts), 0o644); err != nil {
		log.Printf("%s: write timestamp: %v", p.Name(), err)
	}
}

// every is how old a source's data may get before it is refreshed: the
// configured interval, or the source's own floor where that is longer.
func (r *Registry) every(p Provider) time.Duration {
	every := r.interval
	if every <= 0 {
		every = fallbackInterval
	}
	if floor := p.MinRefresh(); floor > every {
		every = floor
	}
	return every
}

func (r *Registry) needsUpdate(p Provider) bool {
	if !p.FilesPresent() {
		return true
	}
	data, err := os.ReadFile(r.stampPath(p))
	if err != nil {
		return true
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return true
	}
	return time.Since(time.Unix(ts, 0)) > r.every(p)
}

// holdoff is how long a failing source is left alone. After a failure it
// waits one check interval, and each failure after that doubles the wait, up
// to the source's refresh interval. Downloads count against limits — IPinfo
// allows ten a day, and MaxMind counts every one — and a source that is down
// should not be asked four times an hour until it comes back.
type holdoff struct {
	failures int
	until    time.Time
}

// failed records a failure of p and returns how long p is now left alone.
func (r *Registry) failed(p Provider) time.Duration {
	h := r.held[p]
	wait := r.check << min(h.failures, 20)
	if every := r.every(p); wait <= 0 || wait > every {
		wait = every
	}
	h.failures++
	h.until = time.Now().Add(wait)
	r.held[p] = h
	return wait
}

// holding reports whether p failed recently enough to be left alone for now.
func (r *Registry) holding(p Provider) bool { return time.Now().Before(r.held[p].until) }

// RefreshLoop brings up the sources that are not answering yet and refreshes
// the ones that are due, at once and then every check interval. A failure is
// logged and tried again after its holdoff; open readers keep serving.
func (r *Registry) RefreshLoop(ctx context.Context) {
	check := time.NewTicker(r.check)
	defer check.Stop()
	for {
		r.retryPending()
		r.refreshLive()
		select {
		case <-ctx.Done():
			log.Println("database update task cancelled")
			return
		case <-check.C:
		}
	}
}

// retryPending brings up every source not answering yet: a first download
// deferred at startup, or a source lost to a failure.
func (r *Registry) retryPending() {
	r.mu.RLock()
	pending := append([]Provider(nil), r.pending...)
	r.mu.RUnlock()
	for _, p := range pending {
		if r.holding(p) {
			continue
		}
		if err := r.bring(p); err != nil {
			log.Printf("%s: still unavailable, next try in %s: %v", p.Name(), r.failed(p), err)
			continue
		}
		delete(r.held, p)
		r.promote(p)
		log.Printf("%s: ready and now answering", p.Name())
	}
}

// refreshLive re-checks each answering source whose data is due.
func (r *Registry) refreshLive() {
	for _, p := range r.live() {
		if !r.needsUpdate(p) || r.holding(p) {
			continue
		}
		log.Printf("%s: refreshing databases...", p.Name())
		if err := p.Download(); err != nil {
			if errors.Is(err, errNotModified) {
				// Stamped like a download: the question the stamp answers is
				// "when did this source last check out", and it just did.
				delete(r.held, p)
				r.stamp(p)
				log.Printf("%s: already current", p.Name())
				continue
			}
			log.Printf("%s: refresh failed, next try in %s: %v", p.Name(), r.failed(p), err)
			continue
		}
		if err := p.Open(); err != nil {
			// The freshly downloaded files are on disk but unusable. Leave
			// the stamp alone so a later check retries rather than trusting
			// them; the previously open readers keep serving meanwhile.
			log.Printf("%s: reopen after refresh failed, next try in %s: %v", p.Name(), r.failed(p), err)
			continue
		}
		delete(r.held, p)
		r.stamp(p)
		log.Printf("%s: databases updated", p.Name())
	}
}

// DefaultClient is the client every source downloads with.
//
// IPinfo's token rides in the query string, and IPinfo answers a download with
// a redirect to signed storage on its CDN. Following a redirect, Go sends the
// previous URL as the Referer, query string and all, which would hand the token
// to the storage host. It sets that header before consulting CheckRedirect, so
// this is where it comes off. (MaxMind's key travels as basic auth, which Go
// itself drops on a redirect to another host.) The ten-redirect limit is Go's
// default, kept.
func DefaultClient() *http.Client {
	return &http.Client{
		Timeout: downloadTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			req.Header.Del("Referer")
			return nil
		},
	}
}
