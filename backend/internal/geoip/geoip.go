package geoip

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	geoip2 "github.com/oschwald/geoip2-golang"

	"beacon/internal/config"
)

const (
	maxMMDBBytes      = 512 << 20
	maxArchiveMembers = 64
	fallbackInterval  = 12 * time.Hour
)

type Result struct {
	City        string
	Country     string
	CountryCode string
	ASN         string
}

type readers struct {
	country *geoip2.Reader
	city    *geoip2.Reader
	asn     *geoip2.Reader
}

func openReaders(cfg config.Config) (*readers, error) {
	country, err := geoip2.Open(cfg.CountryDBPath())
	if err != nil {
		return nil, fmt.Errorf("open country db: %w", err)
	}
	city, err := geoip2.Open(cfg.CityDBPath())
	if err != nil {
		country.Close()
		return nil, fmt.Errorf("open city db: %w", err)
	}
	asn, err := geoip2.Open(cfg.ASNDBPath())
	if err != nil {
		country.Close()
		city.Close()
		return nil, fmt.Errorf("open asn db: %w", err)
	}
	return &readers{country: country, city: city, asn: asn}, nil
}

func (r *readers) close() {
	if r == nil {
		return
	}
	for _, rd := range []*geoip2.Reader{r.country, r.city, r.asn} {
		if rd != nil {
			rd.Close()
		}
	}
}

type GeoIP struct {
	cfg        config.Config
	httpClient *http.Client

	mu  sync.RWMutex
	dbs *readers
}

func New(cfg config.Config) (*GeoIP, error) {
	g := &GeoIP{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}

	if !g.dbFilesPresent() {
		if err := g.download(); err != nil {
			return nil, fmt.Errorf("download geoip databases: %w", err)
		}
	}

	dbs, err := openReaders(cfg)
	if err != nil {
		return nil, err
	}
	g.dbs = dbs
	return g, nil
}

func (g *GeoIP) dbFilesPresent() bool {
	for _, p := range g.cfg.DBPaths() {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

func (g *GeoIP) needsUpdate() bool {
	data, err := os.ReadFile(g.cfg.TimestampPath())
	if err != nil {
		return true
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return true
	}
	if time.Since(time.Unix(ts, 0)) > time.Duration(g.cfg.UpdatePeriodHours)*time.Hour {
		return true
	}
	if !g.dbFilesPresent() {
		return true
	}
	return false
}

func (g *GeoIP) download() error {
	if err := os.MkdirAll(g.cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	for _, p := range g.cfg.DBPaths() {
		os.Remove(p + ".tmp")
	}
	for _, u := range g.cfg.DownloadURLs() {
		if err := g.downloadOne(u); err != nil {
			return err
		}
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	if err := os.WriteFile(g.cfg.TimestampPath(), []byte(ts), 0o644); err != nil {
		return fmt.Errorf("write timestamp: %w", err)
	}
	return nil
}

type pendingFile struct {
	tmp  string
	dest string
}

func (g *GeoIP) downloadOne(rawURL string) error {
	hasher := sha256.New()
	pending, err := g.fetchArchive(rawURL, hasher)
	if err != nil {
		cleanupPending(pending)
		return err
	}

	expected, err := g.fetchChecksum(checksumURL(rawURL))
	if err != nil {
		cleanupPending(pending)
		return err
	}

	if got := hex.EncodeToString(hasher.Sum(nil)); !strings.EqualFold(got, expected) {
		cleanupPending(pending)
		return fmt.Errorf("checksum mismatch for %s", redactURL(rawURL))
	}

	for i, p := range pending {
		if err := os.Rename(p.tmp, p.dest); err != nil {
			cleanupPending(pending[i:])
			return fmt.Errorf("install %s: %w", p.dest, err)
		}
	}
	return nil
}

func (g *GeoIP) fetchArchive(rawURL string, hasher io.Writer) ([]pendingFile, error) {
	resp, err := g.get(rawURL)
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
		dest := filepath.Join(g.cfg.DataDir, filepath.Base(hdr.Name))
		tmp := dest + ".tmp"
		if err := writeMMDB(tmp, tr, maxMMDBBytes); err != nil {
			cleanupPending(pending)
			return nil, err
		}
		pending = append(pending, pendingFile{tmp: tmp, dest: dest})
	}

	if _, err := io.Copy(io.Discard, tee); err != nil {
		cleanupPending(pending)
		return nil, fmt.Errorf("read archive %s: %w", redactURL(rawURL), err)
	}
	return pending, nil
}

func (g *GeoIP) fetchChecksum(rawURL string) (string, error) {
	resp, err := g.get(rawURL)
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

func (g *GeoIP) get(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", redactURL(rawURL), sanitizeURLErr(err))
	}
	req.SetBasicAuth(g.cfg.AccountID, g.cfg.LicenseKey)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", redactURL(rawURL), sanitizeURLErr(err))
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: status %d", redactURL(rawURL), resp.StatusCode)
	}
	return resp, nil
}

func writeMMDB(tmp string, src io.Reader, limit int64) error {
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	n, copyErr := io.Copy(f, io.LimitReader(src, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, copyErr)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, closeErr)
	}
	if n > limit {
		os.Remove(tmp)
		return fmt.Errorf("database %s exceeds %d bytes", filepath.Base(tmp), limit)
	}
	return nil
}

func cleanupPending(pending []pendingFile) {
	for _, p := range pending {
		os.Remove(p.tmp)
	}
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

func sanitizeURLErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<url>"
	}
	q := u.Query()
	if q.Has("license_key") {
		q.Set("license_key", "REDACTED")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (g *GeoIP) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dbs.close()
	g.dbs = nil
}

func (g *GeoIP) UpdateIfNeeded() (bool, error) {
	if !g.needsUpdate() {
		return false, nil
	}
	if err := g.download(); err != nil {
		return false, err
	}
	next, err := openReaders(g.cfg)
	if err != nil {
		return false, err
	}

	g.mu.Lock()
	old := g.dbs
	g.dbs = next
	old.close()
	g.mu.Unlock()

	return true, nil
}

func (g *GeoIP) RefreshLoop(ctx context.Context) {
	interval := time.Duration(g.cfg.UpdatePeriodHours) * time.Hour
	if interval <= 0 {
		interval = fallbackInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		log.Println("checking if GeoIP databases need updating...")
		updated, err := g.UpdateIfNeeded()
		switch {
		case err != nil:
			log.Printf("error updating GeoIP databases: %v", err)
		case updated:
			log.Println("GeoIP databases updated successfully")
		default:
			log.Println("GeoIP databases are up to date")
		}

		select {
		case <-ctx.Done():
			log.Println("database update task cancelled")
			return
		case <-ticker.C:
		}
	}
}

func (g *GeoIP) Lookup(ipStr string) Result {
	var res Result

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return res
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.dbs == nil {
		return res
	}

	if rec, err := g.dbs.asn.ASN(ip); err == nil {
		res.ASN = formatASN(rec.AutonomousSystemNumber, rec.AutonomousSystemOrganization)
	}

	if rec, err := g.dbs.city.City(ip); err == nil && cityRecordHasData(rec) {
		res.City = rec.City.Names["en"]
		res.Country = rec.Country.Names["en"]
		res.CountryCode = rec.Country.IsoCode
		return res
	}

	if rec, err := g.dbs.country.Country(ip); err == nil {
		res.Country = rec.Country.Names["en"]
		res.CountryCode = rec.Country.IsoCode
	}

	return res
}

func formatASN(number uint, org string) string {
	if number == 0 {
		return ""
	}
	if org != "" {
		return fmt.Sprintf("AS%d (%s)", number, org)
	}
	return fmt.Sprintf("AS%d", number)
}

func cityRecordHasData(rec *geoip2.City) bool {
	return rec.Country.IsoCode != "" ||
		len(rec.Country.Names) > 0 ||
		len(rec.City.Names) > 0
}
