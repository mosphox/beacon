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
	maxmindBaseURL = "https://download.maxmind.com/app/geoip_download"

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

	dbs swappable
}

func NewMaxMind(accountID, licenseKey, dataDir string, client *http.Client) *MaxMind {
	return &MaxMind{
		accountID:  accountID,
		licenseKey: licenseKey,
		dataDir:    dataDir,
		client:     client,
	}
}

func (m *MaxMind) Name() string { return "MaxMind" }

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

// Download fetches all three editions before installing any of them, so a
// failure on the last one cannot leave a mixed-vintage set on disk.
func (m *MaxMind) Download() error {
	removeStaleTemps(sliceOf(m.paths())...)

	var pending []pendingFile
	for _, edition := range []string{mmCountryEdition, mmCityEdition, mmASNEdition} {
		p, err := m.fetchEdition(edition)
		if err != nil {
			cleanupPending(pending)
			return err
		}
		pending = append(pending, p...)
	}
	return install(pending)
}

func (m *MaxMind) fetchEdition(edition string) ([]pendingFile, error) {
	rawURL := fmt.Sprintf("%s?edition_id=%s&license_key=%s&suffix=tar.gz",
		maxmindBaseURL, edition, m.licenseKey)

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
		n, err := writeMMDBN(tmp, tr, limit)
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

// MaxMind publishes on a fixed cadence; there is no month-stamped URL to fall
// back to, so this is just a marker for the registry's staleness check.
func (m *MaxMind) MinRefresh() time.Duration { return 0 }

func sliceOf(a, b, c string) []string { return []string{a, b, c} }
