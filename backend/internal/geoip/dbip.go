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

	dbs swappable
}

func NewDBIP(dataDir string, client *http.Client) *DBIP {
	return &DBIP{dataDir: dataDir, client: client}
}

func (d *DBIP) Name() string { return "DB-IP" }

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
// new ones have not been published yet.
func (d *DBIP) Download() error {
	city, asn := d.paths()
	removeStaleTemps(city, asn)

	now := time.Now().UTC()
	var pending []pendingFile

	for _, ed := range []struct{ edition, dest string }{
		{"city", city},
		{"asn", asn},
	} {
		p, err := d.fetchEdition(ed.edition, ed.dest, now)
		if err != nil {
			cleanupPending(pending)
			return err
		}
		pending = append(pending, p)
	}

	return install(pending)
}

func (d *DBIP) fetchEdition(edition, dest string, now time.Time) (pendingFile, error) {
	for _, offset := range []int{0, -1} {
		stamp := now.AddDate(0, offset, 0).Format("2006-01")
		rawURL := fmt.Sprintf("%s/dbip-%s-lite-%s.mmdb.gz", dbipBaseURL, edition, stamp)

		p, err := fetchGzippedMMDB(d.client, rawURL, dest)
		if err == nil {
			if offset != 0 {
				log.Printf("DB-IP: %s for %s not published yet, using %s", edition, now.Format("2006-01"), stamp)
			}
			return p, nil
		}
		if !isUnavailable(err) {
			return pendingFile{}, err
		}
	}
	return pendingFile{}, fmt.Errorf("DB-IP %s: no published database for %s or the month before",
		edition, now.Format("2006-01"))
}

// DB-IP publishes monthly, so checking more often than daily is pointless.
func (d *DBIP) MinRefresh() time.Duration { return 24 * time.Hour }
