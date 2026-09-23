package geoip

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

const dbipBaseURL = "https://download.db-ip.com/free"

// DBIP serves DB-IP Lite: free, no account, CC-BY 4.0, published monthly at a
// month-stamped URL. There is no published checksum, and no Country edition is
// needed because the City database carries country data.
type DBIP struct {
	dataDir string
	client  *http.Client
	baseURL string

	dbs swappable
}

func NewDBIP(dataDir string, client *http.Client) *DBIP {
	return &DBIP{dataDir: dataDir, client: client, baseURL: dbipBaseURL}
}

func (d *DBIP) Name() string { return "DB-IP" }

// Provides is what the Lite editions carry: no postal code, accuracy radius,
// time zone, metro code, registered country or traits, for any address.
func (d *DBIP) Provides() Fields {
	return FieldCity | FieldRegion | FieldCoordinates | FieldCountry | FieldContinent |
		FieldEuropeanUnion | FieldASN | FieldASNOrg
}

func (d *DBIP) paths() (city, asn string) {
	return filepath.Join(d.dataDir, "dbip-city-lite.mmdb"),
		filepath.Join(d.dataDir, "dbip-asn-lite.mmdb")
}

func (d *DBIP) FilesPresent() bool {
	city, asn := d.paths()
	return filesPresent(city, asn)
}

func (d *DBIP) Lookup(ip net.IP) Record { return d.dbs.lookup(ip) }

func (d *DBIP) Open() error {
	city, asn := d.paths()
	set, err := openSet("", city, asn)
	if err != nil {
		return err
	}
	d.dbs.swap(set)
	return nil
}

func (d *DBIP) Close() { d.dbs.closeAll() }

// Download fetches this month's files, falling back to last month's when the
// new ones have not been published yet — and only a file that is not the one
// installed. A HEAD request says which release is published; DB-IP publishes
// once a month, so nearly every check ends there rather than downloading the
// same 137 MB again.
func (d *DBIP) Download() error {
	city, asn := d.paths()
	removeStaleTemps(city, asn)

	now := time.Now().UTC()
	var pending []pendingFile

	for _, ed := range []struct{ edition, dest string }{
		{"city", city},
		{"asn", asn},
	} {
		rawURL, released, err := d.latest(ed.edition, now)
		if err != nil {
			cleanupPending(pending)
			return err
		}
		if isRelease(ed.dest, released) {
			continue
		}
		p, err := fetchGzippedMMDB(d.client, rawURL, ed.dest)
		if err != nil {
			cleanupPending(pending)
			return err
		}
		markRelease(p.tmp, released)
		pending = append(pending, p)
	}
	if len(pending) == 0 {
		return errNotModified
	}
	return install(pending)
}

// latest finds an edition's newest published file: this month's, or last
// month's while this month's is not out. It returns the file's URL and when it
// was published.
func (d *DBIP) latest(edition string, now time.Time) (string, time.Time, error) {
	// From the first of the month: a month back from the 31st of March is the
	// 3rd of March, not February.
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, month := range []time.Time{thisMonth, thisMonth.AddDate(0, -1, 0)} {
		stamp := month.Format("2006-01")
		rawURL := fmt.Sprintf("%s/dbip-%s-lite-%s.mmdb.gz", d.baseURL, edition, stamp)

		released, err := headModified(d.client, rawURL, "", "")
		if err == nil {
			if month != thisMonth {
				log.Printf("DB-IP: %s for %s not published yet, using %s", edition, now.Format("2006-01"), stamp)
			}
			return rawURL, released, nil
		}
		if !isUnavailable(err) {
			return "", time.Time{}, err
		}
	}
	return "", time.Time{}, fmt.Errorf("DB-IP %s: no published database for %s or the month before",
		edition, now.Format("2006-01"))
}

// DB-IP publishes monthly, so checking more often than daily is pointless,
// even though a check is only a HEAD request.
func (d *DBIP) MinRefresh() time.Duration { return 24 * time.Hour }
