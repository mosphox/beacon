package geoip

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const (
	// The permalinks MaxMind's documentation gives, authenticated with the
	// account ID and licence key as basic auth — which keeps the key out of the
	// URL, and so out of logs and the Referer of the redirect to storage.
	maxmindBaseURL = "https://download.maxmind.com/geoip/databases"

	mmCountryEdition = "GeoLite2-Country"
	mmCityEdition    = "GeoLite2-City"
	mmASNEdition     = "GeoLite2-ASN"
)

// MaxMind serves GeoLite2, which needs an account and ships tar.gz archives
// with a published SHA-256.
type MaxMind struct {
	accountID  string
	licenseKey string
	dataDir    string
	client     *http.Client
	baseURL    string

	dbs swappable
}

func NewMaxMind(accountID, licenseKey, dataDir string, client *http.Client) *MaxMind {
	return &MaxMind{
		accountID:  accountID,
		licenseKey: licenseKey,
		dataDir:    dataDir,
		client:     client,
		baseURL:    maxmindBaseURL,
	}
}

func (m *MaxMind) Name() string { return "MaxMind" }

func (m *MaxMind) Provides() Fields { return AllFields }

func (m *MaxMind) paths() (country, city, asn string) {
	return filepath.Join(m.dataDir, mmCountryEdition+".mmdb"),
		filepath.Join(m.dataDir, mmCityEdition+".mmdb"),
		filepath.Join(m.dataDir, mmASNEdition+".mmdb")
}

func (m *MaxMind) FilesPresent() bool { return filesPresent(sliceOf(m.paths())...) }

func (m *MaxMind) Lookup(ip net.IP) Record { return m.dbs.lookup(ip) }

func (m *MaxMind) Open() error {
	country, city, asn := m.paths()
	set, err := openSet(country, city, asn)
	if err != nil {
		return err
	}
	m.dbs.swap(set)
	return nil
}

func (m *MaxMind) Close() { m.dbs.closeAll() }

// Download asks when each edition's latest build was published and fetches
// only the editions that have moved. The asking is a HEAD request, which
// MaxMind does not count against the daily download limit; every GET counts.
// City and Country are rebuilt twice a week and ASN most days, so fetching all
// three on every refresh spent the limit on data already here.
//
// Whatever is fetched is downloaded and verified before any of it is
// installed, so a failure part-way cannot leave half a refresh on disk.
func (m *MaxMind) Download() error {
	country, city, asn := m.paths()
	removeStaleTemps(country, city, asn)

	var pending []pendingFile
	for _, ed := range []struct{ edition, dest string }{
		{mmCountryEdition, country},
		{mmCityEdition, city},
		{mmASNEdition, asn},
	} {
		rawURL := fmt.Sprintf("%s/%s/download?suffix=tar.gz", m.baseURL, ed.edition)
		released, err := headModified(m.client, rawURL, m.accountID, m.licenseKey)
		if err != nil {
			cleanupPending(pending)
			return err
		}
		if isRelease(ed.dest, released) {
			continue
		}
		p, err := m.fetchEdition(rawURL)
		if err != nil {
			cleanupPending(pending)
			return err
		}
		for _, f := range p {
			markRelease(f.tmp, released)
		}
		pending = append(pending, p...)
	}
	if len(pending) == 0 {
		return errNotModified
	}
	return install(pending)
}

func (m *MaxMind) fetchEdition(rawURL string) ([]pendingFile, error) {
	hasher := sha256.New()
	pending, err := m.fetchArchive(rawURL, hasher)
	if err != nil {
		cleanupPending(pending)
		return nil, err
	}

	expected, err := m.fetchChecksum(checksumURL(rawURL))
	if err != nil {
		cleanupPending(pending)
		return nil, err
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); !strings.EqualFold(got, expected) {
		cleanupPending(pending)
		return nil, fmt.Errorf("checksum mismatch for %s", redactURL(rawURL))
	}

	return pending, nil
}

func (m *MaxMind) fetchArchive(rawURL string, hasher io.Writer) ([]pendingFile, error) {
	resp, err := httpGet(m.client, rawURL, m.accountID, m.licenseKey)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	tee := io.TeeReader(resp.Body, hasher)
	gz, err := gzip.NewReader(tee)
	if err != nil {
		return nil, fmt.Errorf("gzip %s: %w", redactURL(rawURL), err)
	}
	defer gz.Close()

	var pending []pendingFile
	tr := tar.NewReader(gz)
	members := 0
	budget := int64(maxArchiveBytes)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanupPending(pending)
			return nil, fmt.Errorf("read archive %s: %w", redactURL(rawURL), err)
		}
		members++
		if members > maxArchiveMembers {
			cleanupPending(pending)
			return nil, fmt.Errorf("archive %s has too many members", redactURL(rawURL))
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".mmdb") {
			continue
		}
		limit := budget
		if limit > maxMMDBBytes {
			limit = maxMMDBBytes
		}
		dest := filepath.Join(m.dataDir, filepath.Base(hdr.Name))
		tmp := dest + ".tmp"
		n, err := writeDBN(tmp, tr, limit)
		if err != nil {
			cleanupPending(pending)
			return nil, err
		}
		budget -= n
		pending = append(pending, pendingFile{tmp: tmp, dest: dest})
	}

	// The hash covers the whole response, so drain whatever the tar reader
	// left behind before comparing checksums.
	if _, err := io.Copy(io.Discard, tee); err != nil {
		cleanupPending(pending)
		return nil, fmt.Errorf("read archive %s: %w", redactURL(rawURL), err)
	}
	return pending, nil
}

func (m *MaxMind) fetchChecksum(rawURL string) (string, error) {
	resp, err := httpGet(m.client, rawURL, m.accountID, m.licenseKey)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	line, err := bufio.NewReader(io.LimitReader(resp.Body, 1024)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read checksum %s: %w", redactURL(rawURL), err)
	}
	fields := strings.Fields(line)
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", fmt.Errorf("invalid checksum for %s", redactURL(rawURL))
	}
	return fields[0], nil
}

func checksumURL(rawURL string) string {
	if strings.Contains(rawURL, "suffix=tar.gz") {
		return strings.Replace(rawURL, "suffix=tar.gz", "suffix=tar.gz.sha256", 1)
	}
	if strings.Contains(rawURL, "?") {
		return rawURL + "&suffix=tar.gz.sha256"
	}
	return rawURL + "?suffix=tar.gz.sha256"
}

// Checking costs three HEAD requests, which MaxMind does not count; only an
// edition that has moved is downloaded.
func (m *MaxMind) MinRefresh() time.Duration { return 0 }

func sliceOf(a, b, c string) []string { return []string{a, b, c} }
