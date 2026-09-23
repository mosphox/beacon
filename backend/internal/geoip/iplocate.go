package geoip

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

const (
	iplocatePointerBase = "https://raw.githubusercontent.com/iplocate/ip-address-databases/main/"
	iplocateObjectBase  = "https://media.githubusercontent.com/media/iplocate/ip-address-databases/main/"

	iplocateCountryPath = "ip-to-country/ip-to-country.mmdb"
	iplocateASNPath     = "ip-to-asn/ip-to-asn.mmdb"

	// A Git LFS pointer is three short lines.
	maxPointerBytes = 1024
)

// IPLocate serves IPLocate.io's free IP-to-Country and IP-to-ASN databases: no
// account, CC BY-SA 4.0, rebuilt daily from IPLocate's own data.
//
// They are published through Git LFS, so the file checked into the repository
// is a pointer carrying the SHA-256 and size of the current database. That one
// small request is both the change check — a refresh downloads nothing when
// the hash matches what is installed — and the integrity check for whatever it
// does download.
type IPLocate struct {
	dataDir     string
	client      *http.Client
	pointerBase string
	objectBase  string

	dbs swappable
}

func NewIPLocate(dataDir string, client *http.Client) *IPLocate {
	return &IPLocate{
		dataDir:     dataDir,
		client:      client,
		pointerBase: iplocatePointerBase,
		objectBase:  iplocateObjectBase,
	}
}

func (p *IPLocate) Name() string { return "IPLocate" }

func (p *IPLocate) Provides() Fields {
	return FieldCountry | FieldContinent | FieldASN | FieldASNOrg
}

func (p *IPLocate) paths() (country, asn string) {
	return filepath.Join(p.dataDir, "iplocate-country.mmdb"),
		filepath.Join(p.dataDir, "iplocate-asn.mmdb")
}

func (p *IPLocate) FilesPresent() bool { return filesPresent(sliceOf2(p.paths())...) }

func (p *IPLocate) Lookup(ip net.IP) Record { return p.dbs.lookup(ip) }

func (p *IPLocate) Open() error {
	countryPath, asnPath := p.paths()
	country, err := maxminddb.Open(countryPath)
	if err != nil {
		return fmt.Errorf("open country db: %w", err)
	}
	asn, err := maxminddb.Open(asnPath)
	if err != nil {
		country.Close()
		return fmt.Errorf("open asn db: %w", err)
	}
	p.dbs.swap(&iplocateSet{country: country, asn: asn})
	return nil
}

func (p *IPLocate) Close() { p.dbs.closeAll() }

// Checking costs one small request per file, so there is no reason to wait
// longer than the configured interval.
func (p *IPLocate) MinRefresh() time.Duration { return 0 }

func (p *IPLocate) Download() error {
	countryPath, asnPath := p.paths()
	removeStaleTemps(countryPath, asnPath)

	var pending []pendingFile
	for _, f := range []struct{ repoPath, dest string }{
		{iplocateCountryPath, countryPath},
		{iplocateASNPath, asnPath},
	} {
		text, err := fetchText(p.client, p.pointerBase+f.repoPath, maxPointerBytes)
		if err != nil {
			cleanupPending(pending)
			return err
		}
		ptr, err := parseLFSPointer(text)
		if err != nil {
			cleanupPending(pending)
			return fmt.Errorf("IPLocate %s: %w", f.repoPath, err)
		}
		if have, err := fileSHA256(f.dest); err == nil && strings.EqualFold(have, ptr.sha256) {
			continue
		}
		pf, err := fetchVerified(p.client, p.objectBase+f.repoPath, f.dest, ptr.sha256, ptr.size, maxMMDBBytes)
		if err != nil {
			cleanupPending(pending)
			return err
		}
		pending = append(pending, pf)
	}

	if len(pending) == 0 {
		return errNotModified
	}
	return install(pending)
}

// lfsPointer is the part of a Git LFS pointer file that identifies an object.
type lfsPointer struct {
	sha256 string
	size   int64
}

// parseLFSPointer reads the pointer format from the Git LFS specification:
//
//	version https://git-lfs.github.com/spec/v1
//	oid sha256:<64 hex digits>
//	size <bytes>
func parseLFSPointer(text string) (lfsPointer, error) {
	var ptr lfsPointer
	ptr.size = -1
	versioned := false
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		switch key {
		case "version":
			versioned = strings.HasPrefix(value, "https://git-lfs.github.com/spec/")
		case "oid":
			if hash, found := strings.CutPrefix(value, "sha256:"); found && isSHA256(hash) {
				ptr.sha256 = strings.ToLower(hash)
			}
		case "size":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil && n > 0 {
				ptr.size = n
			}
		}
	}
	if !versioned || ptr.sha256 == "" || ptr.size < 0 {
		return lfsPointer{}, fmt.Errorf("not a Git LFS pointer")
	}
	return ptr, nil
}

// The records are flat maps, not the nested GeoIP2 layout. geoip2-golang reads
// such a file without complaint and returns an empty record for every address,
// which is why these are decoded with maxminddb-golang into structs of their own.
type iplocateCountry struct {
	CountryCode   string `maxminddb:"country_code"`
	CountryName   string `maxminddb:"country_name"`
	ContinentCode string `maxminddb:"continent_code"`
}

type iplocateASN struct {
	// A decimal string, "15169", not a number.
	ASN  string `maxminddb:"asn"`
	Org  string `maxminddb:"org"`
	Name string `maxminddb:"name"`
}

type iplocateSet struct {
	country *maxminddb.Reader
	asn     *maxminddb.Reader
}

func (s *iplocateSet) lookup(ip net.IP) Record {
	var rec Record
	if s == nil {
		return rec
	}

	if s.country != nil {
		var c iplocateCountry
		if err := s.country.Lookup(ip, &c); err == nil {
			fillIPLocateCountry(&rec, c)
		}
	}
	if s.asn != nil {
		var a iplocateASN
		if err := s.asn.Lookup(ip, &a); err == nil {
			fillIPLocateASN(&rec, a)
		}
	}
	return rec
}

func fillIPLocateCountry(rec *Record, c iplocateCountry) {
	cc := countryCode(c.CountryCode)
	if cc == "" {
		return
	}
	rec.CountryCode = cc
	rec.Country = c.CountryName
	if name, ok := continentNames[c.ContinentCode]; ok {
		rec.ContinentCode = c.ContinentCode
		rec.Continent = name
	}
	rec.HasData = true
}

func fillIPLocateASN(rec *Record, a iplocateASN) {
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToUpper(a.ASN), "AS"), 10, 32)
	if err != nil || n == 0 {
		return
	}
	rec.ASN = uint(n)
	// org is the organisation ("Google LLC"); name is the registry handle
	// ("GOOGLE"), used only when there is no organisation.
	rec.ASNOrg = a.Org
	if rec.ASNOrg == "" {
		rec.ASNOrg = a.Name
	}
	rec.HasData = true
}

func (s *iplocateSet) close() {
	if s == nil {
		return
	}
	for _, rd := range []*maxminddb.Reader{s.country, s.asn} {
		if rd != nil {
			rd.Close()
		}
	}
}

func sliceOf2(a, b string) []string { return []string{a, b} }
