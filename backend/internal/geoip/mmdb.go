package geoip

import (
	"fmt"
	"net"
	"os"
	"sync"

	geoip2 "github.com/oschwald/geoip2-golang"
)

// mmdbSet is the trio of databases a provider publishes. Country is optional:
// a City database already carries country data, and some providers ship only
// City + ASN.
type mmdbSet struct {
	country *geoip2.Reader
	city    *geoip2.Reader
	asn     *geoip2.Reader
}

func openSet(countryPath, cityPath, asnPath string) (*mmdbSet, error) {
	set := &mmdbSet{}
	open := func(path string, dst **geoip2.Reader, what string) error {
		if path == "" {
			return nil
		}
		rd, err := geoip2.Open(path)
		if err != nil {
			return fmt.Errorf("open %s db: %w", what, err)
		}
		*dst = rd
		return nil
	}

	if err := open(countryPath, &set.country, "country"); err != nil {
		set.close()
		return nil, err
	}
	if err := open(cityPath, &set.city, "city"); err != nil {
		set.close()
		return nil, err
	}
	if err := open(asnPath, &set.asn, "asn"); err != nil {
		set.close()
		return nil, err
	}
	return set, nil
}

func (s *mmdbSet) close() {
	if s == nil {
		return
	}
	for _, rd := range []*geoip2.Reader{s.country, s.city, s.asn} {
		if rd != nil {
			rd.Close()
		}
	}
}

// lookup reads one address out of the set. ASN is always attempted; the City
// database supplies location when it has any, otherwise Country is consulted
// for the country alone.
func (s *mmdbSet) lookup(ip net.IP) Record {
	var rec Record
	if s == nil {
		return rec
	}

	if s.asn != nil {
		if r, err := s.asn.ASN(ip); err == nil && r.AutonomousSystemNumber != 0 {
			rec.ASN = r.AutonomousSystemNumber
			rec.ASNOrg = r.AutonomousSystemOrganization
			rec.HasData = true
		}
	}

	if s.city != nil {
		if r, err := s.city.City(ip); err == nil && cityHasData(r) {
			fillFromCity(&rec, r)
			rec.HasData = true
			return rec
		}
	}

	if s.country != nil {
		if r, err := s.country.Country(ip); err == nil && r.Country.IsoCode != "" {
			rec.Country = r.Country.Names["en"]
			rec.CountryCode = r.Country.IsoCode
			rec.Continent = r.Continent.Names["en"]
			rec.ContinentCode = r.Continent.Code
			rec.InEuropeanUnion = r.Country.IsInEuropeanUnion
			rec.RegisteredCountry = r.RegisteredCountry.Names["en"]
			rec.RegisteredCountryCode = r.RegisteredCountry.IsoCode
			rec.IsAnycast = r.Traits.IsAnycast
			rec.IsAnonymousProxy = r.Traits.IsAnonymousProxy
			rec.IsSatelliteProvider = r.Traits.IsSatelliteProvider
			rec.HasData = true
		}
	}

	return rec
}

func fillFromCity(rec *Record, r *geoip2.City) {
	rec.City = r.City.Names["en"]
	rec.Country = r.Country.Names["en"]
	rec.CountryCode = r.Country.IsoCode
	rec.Continent = r.Continent.Names["en"]
	rec.ContinentCode = r.Continent.Code
	rec.PostalCode = r.Postal.Code

	for _, sub := range r.Subdivisions {
		name := sub.Names["en"]
		if name == "" && sub.IsoCode == "" {
			continue
		}
		rec.Subdivisions = append(rec.Subdivisions, Subdivision{Name: name, Code: sub.IsoCode})
	}

	rec.Latitude = r.Location.Latitude
	rec.Longitude = r.Location.Longitude
	rec.AccuracyRadius = r.Location.AccuracyRadius
	rec.TimeZone = r.Location.TimeZone
	rec.MetroCode = r.Location.MetroCode
	// The reader zero-fills an absent location, and 0,0 is a real (if
	// improbable) coordinate, so treat it as present only if something in the
	// location block is non-zero.
	rec.HasCoordinates = r.Location.Latitude != 0 ||
		r.Location.Longitude != 0 ||
		r.Location.AccuracyRadius != 0

	rec.InEuropeanUnion = r.Country.IsInEuropeanUnion
	rec.RegisteredCountry = r.RegisteredCountry.Names["en"]
	rec.RegisteredCountryCode = r.RegisteredCountry.IsoCode

	rec.IsAnycast = r.Traits.IsAnycast
	rec.IsAnonymousProxy = r.Traits.IsAnonymousProxy
	rec.IsSatelliteProvider = r.Traits.IsSatelliteProvider
}

func cityHasData(r *geoip2.City) bool {
	return r.Country.IsoCode != "" || len(r.Country.Names) > 0 || len(r.City.Names) > 0
}

// swappable holds a set of readers that can be replaced while lookups are in
// flight, so a refresh never makes the service stop answering.
type swappable struct {
	mu  sync.RWMutex
	set *mmdbSet
}

// lookup runs under the read lock. The readers are memory-mapped files, so the
// whole read must happen inside the lock: handing the pointer out and releasing
// first would let a refresh close the database mid-lookup.
func (s *swappable) lookup(ip net.IP) Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.set == nil {
		return Record{}
	}
	return s.set.lookup(ip)
}

// swap installs a new set and closes the old one while still holding the write
// lock, which guarantees no reader is inside the set being closed.
func (s *swappable) swap(next *mmdbSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.set
	s.set = next
	old.close()
}

func (s *swappable) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set.close()
	s.set = nil
}

func filesPresent(paths ...string) bool {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}
