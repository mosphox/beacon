// Package rdns resolves PTR records for looked-up addresses.
//
// The address is caller-controlled — anyone can request /<any-ip> — so this is
// an outbound request that a stranger chooses the destination of. Three things
// keep that from turning beacon into a DNS reflector: a hard cap on concurrent
// lookups, deduplication so a burst for one address makes one query, and a
// cache that degrades rather than emptying itself under churn.
//
// A lookup is also on the request path, so every call is bounded by a short
// timeout. Failures are cached too, for a shorter period, because "no PTR
// record" is the common case and is worth remembering.
package rdns

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	negativeTTLDivisor = 4
	defaultMaxEntries  = 8192
	defaultMaxInFlight = 32
	// evictFraction of the cache is dropped when it fills, rather than all of
	// it: clearing everything guarantees a total miss rate under churn, which
	// is exactly when the cache matters most.
	evictFraction = 8
)

type entry struct {
	name    string
	expires time.Time
}

type Resolver struct {
	timeout  time.Duration
	ttl      time.Duration
	max      int
	resolver *net.Resolver

	sem   chan struct{}
	group singleflight.Group

	mu    sync.Mutex
	cache map[string]entry
}

func New(timeout, ttl time.Duration) *Resolver {
	return &Resolver{
		timeout:  timeout,
		ttl:      ttl,
		max:      defaultMaxEntries,
		resolver: &net.Resolver{},
		sem:      make(chan struct{}, defaultMaxInFlight),
		cache:    make(map[string]entry),
	}
}

// Lookup returns the PTR name for ip, or "" if there is none, the lookup fails,
// too many are already in flight, or it does not finish within the timeout.
func (r *Resolver) Lookup(ctx context.Context, ip string) string {
	if r == nil || ip == "" {
		return ""
	}
	if name, ok := r.cached(ip); ok {
		return name
	}

	// Concurrent lookups for the same address collapse into one query, so a
	// burst of requests for one IP cannot multiply into a burst of DNS.
	v, _, _ := r.group.Do(ip, func() (any, error) {
		// Re-check: a duplicate that waited on the leader should take its
		// result rather than issue a second query.
		if name, ok := r.cached(ip); ok {
			return name, nil
		}
		return r.resolve(ctx, ip), nil
	})
	name, _ := v.(string)
	return name
}

func (r *Resolver) resolve(ctx context.Context, ip string) string {
	// Shed load rather than queue: the caller is waiting, and an unbounded
	// number of in-flight queries is the reflector case.
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return ""
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.timeout)
	defer cancel()

	name := ""
	if names, err := r.resolver.LookupAddr(ctx, ip); err == nil && len(names) > 0 {
		name = strings.TrimSuffix(names[0], ".")
	}
	r.store(ip, name)
	return name
}

func (r *Resolver) cached(ip string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[ip]
	if !ok {
		return "", false
	}
	if time.Now().After(e.expires) {
		delete(r.cache, ip)
		return "", false
	}
	return e.name, true
}

func (r *Resolver) store(ip, name string) {
	ttl := r.ttl
	if name == "" {
		ttl /= negativeTTLDivisor
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.cache) >= r.max {
		r.evictLocked()
	}
	r.cache[ip] = entry{name: name, expires: time.Now().Add(ttl)}
}

// evictLocked drops expired entries first, and if that was not enough, a slice
// of arbitrary ones. Map iteration order is randomised, so "arbitrary" is a
// fair sample rather than a biased one.
func (r *Resolver) evictLocked() {
	now := time.Now()
	target := r.max / evictFraction
	if target < 1 {
		target = 1
	}

	dropped := 0
	for k, e := range r.cache {
		if now.After(e.expires) {
			delete(r.cache, k)
			dropped++
		}
	}
	for k := range r.cache {
		if dropped >= target {
			break
		}
		delete(r.cache, k)
		dropped++
	}
}
