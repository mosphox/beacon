package geoip

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ulikunitz/xz"
)

const (
	ipfireURL = "https://location.ipfire.org/databases/1/location.db.xz"

	// The decompressed database is about 50 MB.
	maxLocDBBytes = 512 << 20

	// IPFire compresses with a 64 MiB dictionary (xz -9), which the decoder
	// allocates up front. The stream is what chooses the size, so it is
	// checked before decoding starts; see checkXZDict.
	maxXZDictBytes = 128 << 20
)

// ipfireSigningKey is the ECDSA P-521 key IPFire signs location.db with, from
// libloc's data/signing-key.pem (created 2019-12-10). A database that does not
// verify against it is never installed.
const ipfireSigningKey = `-----BEGIN PUBLIC KEY-----
MIGbMBAGByqGSM49AgEGBSuBBAAjA4GGAAQB1PzZdV9DE59LVCdXy/9cRgvTy9lx
L5tV10awZDilwQ9GR8x/irJE8ctGSnZ5HgbOk+gilurmC5JlJmTjZrW7tt8Awiu8
ir3y2n7XXyiVGIzTHrA6Tw7SG+H9LzuIl0wCg6s6svnXVDyho7b0tSZPUGKMI28q
CUXef0jvZ9+ncTiJh1w=
-----END PUBLIC KEY-----`

// IPFire serves the IPFire Location database: no account, CC BY-SA 4.0,
// rebuilt daily from RIR data, BGP, operators' geofeeds and IPFire's own
// corrections. It is the only free source here that flags anycast, satellite
// and anonymous-proxy networks, which DB-IP Lite never does.
//
// The file is signed by IPFire, and that signature — not the HTTPS connection
// it arrived over — is what decides whether it is installed.
type IPFire struct {
	dataDir string
	client  *http.Client
	url     string
	key     *ecdsa.PublicKey

	dbs swappable
}

func NewIPFire(dataDir string, client *http.Client) *IPFire {
	return &IPFire{
		dataDir: dataDir,
		client:  client,
		url:     ipfireURL,
		key:     mustParseECKey(ipfireSigningKey),
	}
}

func (p *IPFire) Name() string { return "IPFire" }

func (p *IPFire) Provides() Fields {
	return FieldCountry | FieldContinent | FieldASN | FieldASNOrg |
		FieldAnycast | FieldAnonymousProxy | FieldSatelliteProvider
}

func (p *IPFire) path() string { return filepath.Join(p.dataDir, "ipfire-location.db") }

func (p *IPFire) FilesPresent() bool { return filesPresent(p.path()) }

func (p *IPFire) Lookup(ip net.IP) Record { return p.dbs.lookup(ip) }

// Open only parses: the signature was checked before the file was installed,
// and every lookup is bounds-safe against whatever is on disk.
func (p *IPFire) Open() error {
	db, err := openLocDB(p.path())
	if err != nil {
		return err
	}
	p.dbs.swap(db)
	return nil
}

func (p *IPFire) Close() { p.dbs.closeAll() }

// A conditional request answers 304 until IPFire publishes a new database, so
// there is no reason to wait longer than the configured interval.
func (p *IPFire) MinRefresh() time.Duration { return 0 }

// Download asks for the database only if it changed since the installed one,
// whose modification time is set from the server's Last-Modified on install.
func (p *IPFire) Download() error {
	dest := p.path()
	removeStaleTemps(dest)

	var since time.Time
	if fi, err := os.Stat(dest); err == nil {
		since = fi.ModTime()
	}
	resp, err := httpGetSince(p.client, p.url, since)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	br := bufio.NewReaderSize(resp.Body, xzHeaderPeek)
	if err := checkXZDict(br, maxXZDictBytes); err != nil {
		return fmt.Errorf("IPFire: %w", err)
	}
	xr, err := xz.ReaderConfig{SingleStream: true}.NewReader(br)
	if err != nil {
		return fmt.Errorf("IPFire: xz: %w", err)
	}
	tmp := dest + ".tmp"
	if err := writeDB(tmp, xr, maxLocDBBytes); err != nil {
		return fmt.Errorf("IPFire: %w", err)
	}

	created, err := p.check(tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("IPFire: downloaded database rejected: %w", err)
	}
	if lm, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		// Set before the rename so the installed file carries it.
		if err := os.Chtimes(tmp, lm, lm); err != nil {
			log.Printf("IPFire: set modification time: %v", err)
		}
	}
	if err := install([]pendingFile{{tmp: tmp, dest: dest}}); err != nil {
		return err
	}
	log.Printf("IPFire: installed the database built %s", created.Format(time.RFC3339))
	return nil
}

// check opens a downloaded database and verifies IPFire's signature on it.
func (p *IPFire) check(path string) (time.Time, error) {
	db, err := openLocDB(path)
	if err != nil {
		return time.Time{}, err
	}
	defer db.close()
	if err := db.verify(p.key); err != nil {
		return time.Time{}, err
	}
	return db.createdAt, nil
}

func mustParseECKey(pemText string) *ecdsa.PublicKey {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		panic("geoip: signing key is not PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic(fmt.Sprintf("geoip: signing key: %v", err))
	}
	ec, ok := key.(*ecdsa.PublicKey)
	if !ok {
		panic("geoip: signing key is not an ECDSA key")
	}
	return ec
}

// xzHeaderPeek holds the stream header (12 bytes) and the largest block header
// the format allows (1024 bytes).
const xzHeaderPeek = 2048

// checkXZDict refuses an xz stream whose first block asks for an LZMA2
// dictionary larger than limit, without consuming anything from br.
//
// The decoder allocates whatever dictionary a block names before it produces a
// byte, and the downloaded stream is what names it — so the signature check
// that follows decompression cannot protect the memory spent getting there.
// IPFire's stream is a single block. A multi-block stream could still name a
// larger dictionary in a later block; the container's memory limit is the
// backstop for that. Headers are read per the xz file format, sections 2.1.1
// and 3.1; anything that does not parse is left to the decoder to reject.
func checkXZDict(br *bufio.Reader, limit int64) error {
	const streamHeader = 12
	head, err := br.Peek(streamHeader + 1)
	if err != nil {
		return fmt.Errorf("xz: short stream: %w", err)
	}
	if !bytes.Equal(head[:6], []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}) {
		return errors.New("xz: not an xz stream")
	}
	if head[streamHeader] == 0 {
		return errors.New("xz: stream has no blocks")
	}
	size := (int(head[streamHeader]) + 1) * 4
	block, err := br.Peek(streamHeader + size)
	if err != nil {
		return fmt.Errorf("xz: short block header: %w", err)
	}
	b := block[streamHeader : streamHeader+size-4] // without the CRC32

	flags := b[1]
	filters := int(flags&0x03) + 1
	p := 2
	for _, present := range []bool{flags&0x40 != 0, flags&0x80 != 0} { // compressed, uncompressed size
		if present {
			_, n := xzUvarint(b[p:])
			if n == 0 {
				return errors.New("xz: malformed block header")
			}
			p += n
		}
	}
	for range filters {
		id, n := xzUvarint(b[p:])
		if n == 0 {
			return errors.New("xz: malformed filter flags")
		}
		p += n
		propSize, n := xzUvarint(b[p:])
		if n == 0 || uint64(p+n)+propSize > uint64(len(b)) {
			return errors.New("xz: malformed filter flags")
		}
		p += n
		if id == 0x21 && propSize == 1 { // LZMA2
			if dict := lzma2DictSize(b[p]); dict > limit {
				return fmt.Errorf("xz: dictionary of %d bytes exceeds %d", dict, limit)
			}
		}
		p += int(propSize)
	}
	return nil
}

// lzma2DictSize decodes the LZMA2 filter's one property byte.
func lzma2DictSize(bits byte) int64 {
	bits &= 0x3F
	switch {
	case bits > 40:
		return 1 << 62 // invalid; reported as too large
	case bits == 40:
		return 0xFFFFFFFF
	default:
		return int64(2|bits&1) << (bits/2 + 11)
	}
}

// xzUvarint reads an xz multibyte integer: seven bits per byte, low bits first,
// at most nine bytes. A zero length means it did not parse.
func xzUvarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 9; i++ {
		v |= uint64(b[i]&0x7F) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}
