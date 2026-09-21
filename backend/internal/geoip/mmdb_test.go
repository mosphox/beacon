package geoip

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// A refresh swaps readers while requests are in flight.
//
// What actually prevents a lookup from reading a closed memory mapping is
// structural: lookup holds the read lock for the whole read, and swap closes
// the old set while still holding the write lock. This test exercises that
// lock discipline under -race and pins the nil/closed behaviour; it does not
// reproduce the mapping fault itself, because a set built here holds no open
// readers to close.
func TestSwappableConcurrentLookupAndSwap(t *testing.T) {
	var s swappable
	s.swap(&mmdbSet{})

	ip := net.ParseIP("8.8.8.8")
	stop := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s.lookup(ip)
				}
			}
		}()
	}

	for i := 0; i < 200; i++ {
		s.swap(&mmdbSet{})
	}
	close(stop)
	wg.Wait()

	s.closeAll()
	// A lookup after close must return an empty record, not panic.
	if rec := s.lookup(ip); rec.HasData {
		t.Errorf("lookup after close returned data: %+v", rec)
	}
}

func TestSwappableNilSetIsSafe(t *testing.T) {
	var s swappable
	if rec := s.lookup(net.ParseIP("1.1.1.1")); rec.HasData {
		t.Errorf("lookup on empty swappable returned data: %+v", rec)
	}
	s.closeAll() // must not panic on a never-populated set
}

func TestMmdbSetNilLookup(t *testing.T) {
	var set *mmdbSet
	if rec := set.lookup(net.ParseIP("1.1.1.1")); rec.HasData {
		t.Errorf("nil set returned data: %+v", rec)
	}
	set.close() // must not panic
}

// A failed install must leave the previous set intact: a new City database
// beside a stale ASN one is a combination that never existed upstream.
func TestInstallRollsBackOnFailure(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "a.mmdb")
	bad := filepath.Join(dir, "b.mmdb")
	for _, p := range []string{good, bad} {
		if err := os.WriteFile(p, []byte("OLD"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(good+".tmp", []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No .tmp for the second entry, so its rename fails.

	err := install([]pendingFile{
		{tmp: good + ".tmp", dest: good},
		{tmp: bad + ".tmp", dest: bad},
	})
	if err == nil {
		t.Fatal("install succeeded with a missing temp file")
	}

	for _, p := range []string{good, bad} {
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatalf("%s is gone after rollback: %v", filepath.Base(p), readErr)
		}
		if string(b) != "OLD" {
			t.Errorf("%s = %q after rollback, want the previous %q", filepath.Base(p), b, "OLD")
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "*.bak")); len(entries) != 0 {
		t.Errorf("backups left behind: %v", entries)
	}
}

func TestInstallSucceedsAndClearsBackups(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "a.mmdb")
	if err := os.WriteFile(dest, []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".tmp", []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := install([]pendingFile{{tmp: dest + ".tmp", dest: dest}}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "NEW" {
		t.Errorf("dest = %q, want NEW", b)
	}
	if _, err := os.Stat(dest + ".bak"); !os.IsNotExist(err) {
		t.Error("backup not cleaned up after a successful install")
	}
}
