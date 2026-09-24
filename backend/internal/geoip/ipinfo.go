package geoip

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

const (
	ipinfoURL    = "https://ipinfo.io/data/ipinfo_lite.mmdb"
	ipinfoSumURL = "https://ipinfo.io/data/ipinfo_lite.mmdb/checksums"

	// {"checksums": {"md5": "…", "sha1": "…", "sha256": "…"}}, a few hundred bytes.
	maxChecksumBytes = 1024
)

// IPinfo serves IPinfo Lite: country, continent and autonomous system, from
// IPinfo's own measurement network, CC BY-SA 4.0, rebuilt daily.
//
// It needs a free account's token, and IPinfo allows ten downloads a day per
// address. The checksum endpoint does not count against that, so a refresh
// asks it first and downloads only when the published SHA-256 differs from the
// installed file's — which is also what the download is verified against. The
// token travels in the query string, so it is redacted from anything logged.
type IPinfo struct {
	token   string
	dataDir string
	client  *http.Client
	url     string
	sumURL  string

	dbs swappable
}

func NewIPinfo(token, dataDir string, client *http.Client) *IPinfo {
	return &IPinfo{
		token:   token,
		dataDir: dataDir,
		client:  client,
		url:     ipinfoURL,
		sumURL:  ipinfoSumURL,
	}
}

func (p *IPinfo) Name() string { return "IPinfo" }

func (p *IPinfo) Provides() Fields {
	return FieldCountry | FieldContinent | FieldASN | FieldASNOrg
}

func (p *IPinfo) path() string { return filepath.Join(p.dataDir, "ipinfo-lite.mmdb") }

func (p *IPinfo) FilesPresent() bool { return filesPresent(p.path()) }

func (p *IPinfo) Lookup(ip net.IP) Record { return p.dbs.lookup(ip) }

func (p *IPinfo) Open() error {
	rd, err := maxminddb.Open(p.path())
	if err != nil {
		return fmt.Errorf("open lite db: %w", err)
	}
	p.dbs.swap(&ipinfoSet{rd: rd})
	return nil
}

func (p *IPinfo) Close() { p.dbs.closeAll() }

// The checksum request is free and small, so there is no reason to wait longer
// than the configured interval.
func (p *IPinfo) MinRefresh() time.Duration { return 0 }

func (p *IPinfo) withToken(base string) string {
	return base + "?token=" + url.QueryEscape(p.token)
}

func (p *IPinfo) Download() error {
	dest := p.path()
	removeStaleTemps(dest)

	text, err := fetchText(p.client, p.withToken(p.sumURL), maxChecksumBytes)
	if err != nil {
		return err
	}
	// {"checksums": {"md5": "…", "sha1": "…", "sha256": "…"}}
	var sums struct {
		Checksums struct {
			SHA256 string `json:"sha256"`
		} `json:"checksums"`
	}
	if err := json.Unmarshal([]byte(text), &sums); err != nil || !isSHA256(sums.Checksums.SHA256) {
		return fmt.Errorf("IPinfo: no SHA-256 in the checksum response")
	}
	want := strings.ToLower(sums.Checksums.SHA256)

	if have, err := fileSHA256(dest); err == nil && have == want {
		return errNotModified
	}
	// The download answers with a redirect to a signed URL on IPinfo's CDN.
	// The client follows it without passing the token on; see DefaultClient.
	pf, err := fetchVerified(p.client, p.withToken(p.url), dest, want, -1, maxMMDBBytes)
	if err != nil {
		return err
	}
	return install([]pendingFile{pf})
}

// A flat record, as IPLocate's are; see iplocateCountry for why these are not
// read through geoip2-golang. The operator's domain is in the file too, but
// beacon's schema has nowhere to put it yet.
type ipinfoLite struct {
	Country       string `maxminddb:"country"`
	CountryCode   string `maxminddb:"country_code"`
	Continent     string `maxminddb:"continent"`
	ContinentCode string `maxminddb:"continent_code"`
	// "AS15169"; absent for about a fifth of networks.
	ASN    string `maxminddb:"asn"`
	ASName string `maxminddb:"as_name"`
}

type ipinfoSet struct {
	rd *maxminddb.Reader
}

func (s *ipinfoSet) lookup(ip net.IP) Record {
	var rec Record
	if s == nil || s.rd == nil {
		return rec
	}
	var r ipinfoLite
	if err := s.rd.Lookup(ip, &r); err != nil {
		return rec
	}
	fillIPinfo(&rec, r)
	return rec
}

func fillIPinfo(rec *Record, r ipinfoLite) {
	if cc := countryCode(r.CountryCode); cc != "" {
		rec.CountryCode = cc
		rec.Country = r.Country
		if name, ok := continentNames[r.ContinentCode]; ok {
			rec.ContinentCode = r.ContinentCode
			rec.Continent = name
		}
		rec.HasData = true
	}
	if n, ok := parseASN(r.ASN); ok {
		rec.ASN = n
		rec.ASNOrg = r.ASName
		rec.HasData = true
	}
}

func (s *ipinfoSet) close() {
	if s != nil && s.rd != nil {
		s.rd.Close()
	}
}
