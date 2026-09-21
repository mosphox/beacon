package geoip

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	downloadTimeout  = 5 * time.Minute
	fallbackInterval = 12 * time.Hour
)

// Provider is one source of GeoIP data. Each owns its own files in the data
// directory and its own download protocol.
type Provider interface {
	Name() string
	FilesPresent() bool
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

	mu        sync.RWMutex
	providers []Provider // answering lookups
	pending   []Provider // failed to start; retried by the refresh loop
}

func NewRegistry(dataDir string, interval time.Duration, providers ...Provider) (*Registry, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no GeoIP providers enabled")
	}
	// providers is filled by the loop below; seeding it here would double every entry.
	r := &Registry{dataDir: dataDir, interval: interval}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// A provider that cannot be brought up is set aside rather than being fatal,
	// so one failing source never takes the service down when another works. It
	// is retried on every refresh tick: a network blip or a bad file on the
	// volume at boot should not disable a source for the process lifetime.
	for _, p := range providers {
		if err := r.bring(p); err != nil {
			log.Printf("%s: unavailable, will retry: %v", p.Name(), err)
			r.pending = append(r.pending, p)
			continue
		}
		log.Printf("%s: ready", p.Name())
		r.providers = append(r.providers, p)
	}
	if len(r.providers) == 0 {
		return nil, fmt.Errorf("no GeoIP provider could be initialised")
	}
	return r, nil
}

// live returns a snapshot of the answering providers.
func (r *Registry) live() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Provider(nil), r.providers...)
}

// promote moves a provider from pending to answering.
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
}

func (r *Registry) bring(p Provider) error {
	downloaded := false
	if !p.FilesPresent() {
		log.Printf("%s: databases missing, downloading...", p.Name())
		if err := p.Download(); err != nil {
			return err
		}
		downloaded = true
	}
	if err := p.Open(); err != nil {
		return err
	}
	// Stamp only once the files have proven openable, so unusable data is
	// retried rather than treated as fresh.
	if downloaded {
		r.stamp(p)
	}
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
		answers = append(answers, Answer{Source: p.Name(), Record: rec})
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

// stampPath records when a provider last installed fresh data.
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

// RefreshLoop re-checks every provider on the configured interval. A failure
// is logged and retried next tick; the currently open readers keep serving.
func (r *Registry) RefreshLoop(ctx context.Context) {
	interval := r.interval
	if interval <= 0 {
		interval = fallbackInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		// Retry anything that could not start, so a source lost to a transient
		// failure comes back on its own.
		r.mu.RLock()
		pending := append([]Provider(nil), r.pending...)
		r.mu.RUnlock()
		for _, p := range pending {
			if err := r.bring(p); err != nil {
				log.Printf("%s: still unavailable: %v", p.Name(), err)
				continue
			}
			r.promote(p)
			log.Printf("%s: recovered and now answering", p.Name())
		}

		for _, p := range r.live() {
			if !r.needsUpdate(p) {
				continue
			}
			log.Printf("%s: refreshing databases...", p.Name())
			if err := p.Download(); err != nil {
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

		select {
		case <-ctx.Done():
			log.Println("database update task cancelled")
			return
		case <-ticker.C:
		}
	}
}

func DefaultClient() *http.Client { return &http.Client{Timeout: downloadTimeout} }
