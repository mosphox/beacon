package geoip

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

const (
	ipLocationDBURL    = "https://github.com/sapics/ip-location-db/releases/download/latest/user-country.mmdb"
	ipLocationDBSumURL = "https://github.com/sapics/ip-location-db/releases/download/checksum/user-country.mmdb.sha256"

	// "<64 hex digits>  user-country.mmdb\n"
	maxChecksumBytes = 1024
)

// IPLocationDB serves the user-country database from sapics/ip-location-db:
// country only, public domain (PDDL), rebuilt daily.
//
// It is compiled from RIR allocation statistics, BGP routing archives
// (RouteViews, RIPE RIS) and operators' own geofeeds, and deliberately uses no
// WHOIS data and nothing from MaxMind or DB-IP. That is what makes it a source
// rather than a copy of one beacon already has. The same project republishes
// GeoLite2 and DB-IP too; those are not used here for exactly that reason.
type IPLocationDB struct {
	dataDir string
	client  *http.Client
	url     string
	sumURL  string

	dbs swappable
}

func NewIPLocationDB(dataDir string, client *http.Client) *IPLocationDB {
	return &IPLocationDB{
		dataDir: dataDir,
		client:  client,
		url:     ipLocationDBURL,
		sumURL:  ipLocationDBSumURL,
	}
}

func (p *IPLocationDB) Name() string { return "ip-location-db" }

func (p *IPLocationDB) Provides() Fields { return FieldCountry }

func (p *IPLocationDB) path() string {
	return filepath.Join(p.dataDir, "ip-location-db-user-country.mmdb")
}

func (p *IPLocationDB) FilesPresent() bool { return filesPresent(p.path()) }

func (p *IPLocationDB) Lookup(ip net.IP) Record { return p.dbs.lookup(ip) }

func (p *IPLocationDB) Open() error {
	rd, err := maxminddb.Open(p.path())
	if err != nil {
		return fmt.Errorf("open country db: %w", err)
	}
	p.dbs.swap(&ipLocationDBSet{rd: rd})
	return nil
}

func (p *IPLocationDB) Close() { p.dbs.closeAll() }

// The checksum is one small request, so there is no reason to wait longer than
// the configured interval.
func (p *IPLocationDB) MinRefresh() time.Duration { return 0 }

// Download compares the published checksum with the installed file first.
//
// The database and its checksum are separate release assets, uploaded seconds
// apart. A refresh that lands in that gap sees a mismatch, keeps the file it
// has, and succeeds on the next tick.
func (p *IPLocationDB) Download() error {
	dest := p.path()
	removeStaleTemps(dest)

	text, err := fetchText(p.client, p.sumURL, maxChecksumBytes)
	if err != nil {
		return err
	}
	fields := strings.Fields(text)
	if len(fields) == 0 || !isSHA256(fields[0]) {
		return fmt.Errorf("ip-location-db: invalid checksum file")
	}
	want := strings.ToLower(fields[0])

	if have, err := fileSHA256(dest); err == nil && have == want {
		return errNotModified
	}
	pf, err := fetchVerified(p.client, p.url, dest, want, -1, maxMMDBBytes)
	if err != nil {
		return err
	}
	return install([]pendingFile{pf})
}

// A flat record holding only the code; see iplocateCountry for why these are
// not read through geoip2-golang.
type ipLocationDBCountry struct {
	CountryCode string `maxminddb:"country_code"`
}

type ipLocationDBSet struct {
	rd *maxminddb.Reader
}

// lookup fills the code alone. The source names no country, and a name
// supplied here would be beacon's, not the source's.
func (s *ipLocationDBSet) lookup(ip net.IP) Record {
	var rec Record
	if s == nil || s.rd == nil {
		return rec
	}
	var c ipLocationDBCountry
	if err := s.rd.Lookup(ip, &c); err != nil {
		return rec
	}
	if cc := countryCode(c.CountryCode); cc != "" {
		rec.CountryCode = cc
		rec.HasData = true
	}
	return rec
}

func (s *ipLocationDBSet) close() {
	if s != nil && s.rd != nil {
		s.rd.Close()
	}
}
