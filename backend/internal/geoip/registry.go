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

	// retryInterval is how soon a source that could not come up is tried again.
	// The refresh interval suits data that is merely ageing; a source that is
	// missing altogether should not stay missing for twelve hours over one
	// failed download.
	retryInterval = 15 * time.Minute
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
	retry    time.Duration
	rank     map[Provider]int // priority: position in the list given to NewRegistry

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
		retry:    retryInterval,
		rank:     make(map[Provider]int, len(providers)),
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
		log.Printf("%s: unavailable, will retry: %v", p.Name(), err)
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

	age := time.Since(time.Unix(ts, 0))
	every := r.interval
	if floor := p.MinRefresh(); floor > every {
		every = floor
	}
	return age > every
}

// RefreshLoop brings up sources that are not answering yet — at once, then
// every retry interval — and re-checks every answering source on the refresh
// interval. A failure is logged and retried; open readers keep serving.
func (r *Registry) RefreshLoop(ctx context.Context) {
	interval := r.interval
	if interval <= 0 {
		interval = fallbackInterval
	}
	refresh := time.NewTicker(interval)
	defer refresh.Stop()
	retry := time.NewTicker(r.retry)
	defer retry.Stop()

	r.retryPending()
	r.refreshLive()
	for {
		select {
		case <-ctx.Done():
			log.Println("database update task cancelled")
			return
		case <-retry.C:
			r.retryPending()
		case <-refresh.C:
			r.retryPending()
			r.refreshLive()
		}
	}
}

// retryPending brings up every source not answering yet: a first download
// deferred at startup, or a source lost to a transient failure.
func (r *Registry) retryPending() {
	r.mu.RLock()
	pending := append([]Provider(nil), r.pending...)
	r.mu.RUnlock()
	for _, p := range pending {
		if err := r.bring(p); err != nil {
			log.Printf("%s: still unavailable: %v", p.Name(), err)
			continue
		}
		r.promote(p)
		log.Printf("%s: ready and now answering", p.Name())
	}
}

// refreshLive re-checks each answering source whose data is due.
func (r *Registry) refreshLive() {
	for _, p := range r.live() {
		if !r.needsUpdate(p) {
			continue
		}
		log.Printf("%s: refreshing databases...", p.Name())
		if err := p.Download(); err != nil {
			if errors.Is(err, errNotModified) {
				// Stamped like a download: the question the stamp answers is
				// "when did this source last check out", and it just did.
				r.stamp(p)
				log.Printf("%s: already current", p.Name())
				continue
			}
			log.Printf("%s: refresh failed: %v", p.Name(), err)
			continue
		}
		if err := p.Open(); err != nil {
			// The freshly downloaded files are on disk but unusable. Leave
			// the stamp alone so the next tick retries rather than trusting
			// them; the previously open readers keep serving meanwhile.
			log.Printf("%s: reopen after refresh failed: %v", p.Name(), err)
			continue
		}
		r.stamp(p)
		log.Printf("%s: databases updated", p.Name())
	}
}

// DefaultClient is the client every source downloads with.
//
// Credentials ride in query strings — MaxMind's licence key, IPinfo's token —
// and both services answer a download with a redirect to signed storage: an R2
// bucket for MaxMind, IPinfo's CDN. Following a redirect, Go sends the previous
// URL as the Referer, query string and all, which would hand the credential to
// the storage host. It sets that header before consulting CheckRedirect, so this
// is where it comes off. The ten-redirect limit is Go's default, kept.
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
