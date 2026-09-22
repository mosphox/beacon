package rdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// failingResolver counts the queries that reach the network and fails them all.
// That is enough to test everything this package is responsible for: how many
// queries a burst turns into, what happens when it cannot make one, and what it
// remembers afterwards. It needs no DNS server and no network.
func failingResolver(attempts *atomic.Int64) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			attempts.Add(1)
			return nil, errors.New("no dns in tests")
		},
	}
}

func TestNilResolverAndEmptyAddressAreSafe(t *testing.T) {
	var r *Resolver
	if got := r.Lookup(t.Context(), "8.8.8.8"); got != "" {
		t.Errorf("nil resolver returned %q", got)
	}
	if got := New(time.Second, time.Minute).Lookup(t.Context(), ""); got != "" {
		t.Errorf("empty address returned %q", got)
	}
}

// The address is chosen by whoever sends the request, so a burst for one
// address must not become a burst of outbound DNS.
func TestConcurrentLookupsForOneAddressMakeOneQuery(t *testing.T) {
	var attempts atomic.Int64
	r := New(2*time.Second, time.Minute)
	r.resolver = failingResolver(&attempts)

	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Lookup(t.Context(), "198.51.100.1")
		}()
	}
	wg.Wait()

	// PreferGo may try more than one server or protocol for a single lookup;
	// what matters is that 64 callers did not become 64 lookups.
	if n := attempts.Load(); n > 8 {
		t.Errorf("64 concurrent lookups made %d dial attempts; singleflight is not collapsing them", n)
	}
}

// Shed, do not queue. An unbounded number of in-flight lookups is the
// reflector case, and the caller is waiting.
func TestLookupShedsWhenTooManyAreInFlight(t *testing.T) {
	r := New(5*time.Second, time.Minute)
	// Fill the semaphore so no slot is available.
	for range cap(r.sem) {
		r.sem <- struct{}{}
	}

	done := make(chan string, 1)
	go func() { done <- r.Lookup(t.Context(), "198.51.100.2") }()

	select {
	case got := <-done:
		if got != "" {
			t.Errorf("Lookup returned %q while shedding, want empty", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Lookup blocked when it should have shed; the caller is waiting on a request path")
	}
}

// "No PTR record" is the common answer and is worth remembering, but for less
// time than a real one.
func TestFailuresAreCachedForAShorterTime(t *testing.T) {
	var attempts atomic.Int64
	ttl := time.Hour
	r := New(time.Second, ttl)
	r.resolver = failingResolver(&attempts)

	r.Lookup(t.Context(), "198.51.100.3")
	first := attempts.Load()
	if first == 0 {
		t.Fatal("no query was attempted")
	}

	r.Lookup(t.Context(), "198.51.100.3")
	if attempts.Load() != first {
		t.Error("a second lookup re-queried; the negative answer was not cached")
	}

	r.mu.Lock()
	e, ok := r.cache["198.51.100.3"]
	r.mu.Unlock()
	if !ok {
		t.Fatal("nothing cached for a failed lookup")
	}
	if life := time.Until(e.expires); life > ttl/negativeTTLDivisor {
		t.Errorf("negative entry lives %v, want at most %v", life, ttl/negativeTTLDivisor)
	}
}

func TestExpiredEntriesAreNotReturned(t *testing.T) {
	r := New(time.Second, time.Minute)
	r.mu.Lock()
	r.cache["198.51.100.4"] = entry{name: "stale.example.net", expires: time.Now().Add(-time.Second)}
	r.mu.Unlock()

	if name, ok := r.cached("198.51.100.4"); ok {
		t.Errorf("cached returned %q for an expired entry", name)
	}
	r.mu.Lock()
	_, still := r.cache["198.51.100.4"]
	r.mu.Unlock()
	if still {
		t.Error("an expired entry was returned as a miss but left in the map")
	}
}

// Clearing the whole cache guarantees a total miss rate under churn, which is
// exactly when it matters most. Eviction drops a slice.
func TestEvictionDropsAFractionNotEverything(t *testing.T) {
	r := New(time.Second, time.Hour)
	r.max = 64

	for i := range r.max {
		r.store(fmt.Sprintf("198.51.100.%d", i), "host.example.net")
	}
	// One more forces an eviction.
	r.store("203.0.113.1", "host.example.net")

	r.mu.Lock()
	size := len(r.cache)
	r.mu.Unlock()

	kept := r.max - r.max/evictFraction
	if size < kept {
		t.Errorf("cache holds %d after eviction, want at least %d — too much was dropped", size, kept)
	}
	if size > r.max {
		t.Errorf("cache holds %d, above its own max of %d", size, r.max)
	}
}

// A client that disconnects must not abort a lookup other callers are waiting
// on through singleflight.
func TestALookupOutlivesTheRequestThatStartedIt(t *testing.T) {
	var attempts atomic.Int64
	r := New(2*time.Second, time.Minute)
	r.resolver = failingResolver(&attempts)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	r.Lookup(ctx, "198.51.100.5")
	if attempts.Load() == 0 {
		t.Error("an already-cancelled request skipped the query; a disconnect would abandon other waiters")
	}
}
