package geoip

import (
	"net"
	"sync"
)

// database is one opened set of files a provider answers from. A refresh opens
// a new one and swaps it in.
type database interface {
	lookup(ip net.IP) Record
	close()
}

// swappable holds a database that can be replaced while lookups are in flight,
// so a refresh never makes the service stop answering.
type swappable struct {
	mu sync.RWMutex
	db database
}

// lookup runs under the read lock. Every database here is a memory-mapped
// file, so the whole read must happen inside the lock: handing the pointer out
// and releasing first would let a refresh unmap it mid-lookup.
func (s *swappable) lookup(ip net.IP) Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return Record{}
	}
	return s.db.lookup(ip)
}

// swap installs a new database and closes the old one while still holding the
// write lock, which guarantees no reader is inside the one being closed.
func (s *swappable) swap(next database) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.db
	s.db = next
	if old != nil {
		old.close()
	}
}

func (s *swappable) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		s.db.close()
	}
	s.db = nil
}
