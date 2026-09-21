package geoip

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

const (
	maxMMDBBytes      = 512 << 20
	maxArchiveMembers = 64
	// maxArchiveBytes caps a whole archive, not just each member: the member
	// limit alone would allow 64 x 512 MiB onto the data volume before the
	// checksum is even consulted.
	maxArchiveBytes = 1 << 30
)

// errNotFound lets a provider distinguish "this file does not exist yet" from a
// real failure, which matters for month-stamped URLs.
var errNotFound = errors.New("not found")

// pendingFile is a database written to a temp path, not yet installed.
type pendingFile struct {
	tmp  string
	dest string
}

// install renames a batch of downloaded files into place, all or nothing.
//
// A partial install is the dangerous case: a new City database beside a stale
// ASN one is a set that never existed upstream, and a restart in that window
// would open it. Each existing file is moved aside first so that a failure
// part-way through can put the previous set back.
func install(pending []pendingFile) error {
	type restore struct {
		backup, dest string
	}
	var done []restore

	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			os.Remove(done[i].dest)
			if done[i].backup != "" {
				if err := os.Rename(done[i].backup, done[i].dest); err != nil {
					log.Printf("install: rollback of %s failed: %v", done[i].dest, err)
				}
			}
		}
	}

	for i, p := range pending {
		backup := ""
		if _, err := os.Stat(p.dest); err == nil {
			backup = p.dest + ".bak"
			if err := os.Rename(p.dest, backup); err != nil {
				rollback()
				cleanupPending(pending[i:])
				return fmt.Errorf("install %s: set aside previous: %w", p.dest, err)
			}
		}
		if err := os.Rename(p.tmp, p.dest); err != nil {
			if backup != "" {
				if rerr := os.Rename(backup, p.dest); rerr != nil {
					log.Printf("install: restoring %s failed: %v", p.dest, rerr)
				}
			}
			rollback()
			cleanupPending(pending[i:])
			return fmt.Errorf("install %s: %w", p.dest, err)
		}
		done = append(done, restore{backup: backup, dest: p.dest})
	}

	for _, d := range done {
		if d.backup != "" {
			os.Remove(d.backup)
		}
	}
	return nil
}

func cleanupPending(pending []pendingFile) {
	for _, p := range pending {
		os.Remove(p.tmp)
	}
}

// httpGet performs a GET, optionally with basic auth, and reports a 404 as
// errNotFound so callers can fall back.
func httpGet(client *http.Client, rawURL, user, pass string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", redactURL(rawURL), sanitizeURLErr(err))
	}
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", redactURL(rawURL), sanitizeURLErr(err))
	}
	// A month-stamped file that is not published yet reads as 404, and some
	// CDNs answer 403 for the same thing; both mean "try the previous month".
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: status %d: %w", redactURL(rawURL), resp.StatusCode, errNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: status %d", redactURL(rawURL), resp.StatusCode)
	}
	return resp, nil
}

// fetchGzippedMMDB streams a bare .mmdb.gz to a temp file beside dest.
func fetchGzippedMMDB(client *http.Client, rawURL, dest string) (pendingFile, error) {
	resp, err := httpGet(client, rawURL, "", "")
	if err != nil {
		return pendingFile{}, err
	}
	defer resp.Body.Close()

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return pendingFile{}, fmt.Errorf("gzip %s: %w", redactURL(rawURL), err)
	}
	defer gz.Close()

	tmp := dest + ".tmp"
	if err := writeMMDB(tmp, gz, maxMMDBBytes); err != nil {
		return pendingFile{}, err
	}
	return pendingFile{tmp: tmp, dest: dest}, nil
}

func writeMMDB(tmp string, src io.Reader, limit int64) error {
	_, err := writeMMDBN(tmp, src, limit)
	return err
}

// writeMMDBN reports how many bytes were written, so a caller extracting an
// archive can hold the members to a shared budget.
func writeMMDBN(tmp string, src io.Reader, limit int64) (int64, error) {
	f, err := os.Create(tmp)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", tmp, err)
	}
	n, copyErr := io.Copy(f, io.LimitReader(src, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("write %s: %w", tmp, copyErr)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("close %s: %w", tmp, closeErr)
	}
	if n > limit {
		os.Remove(tmp)
		return 0, fmt.Errorf("database %s exceeds %d bytes", filepath.Base(tmp), limit)
	}
	return n, nil
}

func sanitizeURLErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// redactURL removes a MaxMind license key from anything that reaches a log.
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

func removeStaleTemps(paths ...string) {
	for _, p := range paths {
		if p != "" {
			os.Remove(p + ".tmp")
			os.Remove(p + ".bak")
		}
	}
}

// isUnavailable reports a file that is not published yet, as opposed to a
// transport or server failure worth surfacing.
func isUnavailable(err error) bool { return errors.Is(err, errNotFound) }
