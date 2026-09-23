package geoip

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

const (
	ripeInetnumURL  = "https://ftp.ripe.net/ripe/dbase/split/ripe.db.inetnum.gz"
	ripeInet6numURL = "https://ftp.ripe.net/ripe/dbase/split/ripe.db.inet6num.gz"

	// A dump yielding fewer blocks than this is truncated or broken, not a
	// registry that shrank overnight: RIPE holds about 4.1 million IPv4 blocks
	// and 970 thousand IPv6 ones.
	ripeMinBlocks4 = 1_000_000
	ripeMinBlocks6 = 200_000

	// Room reserved for the blocks up front. Grown by appending instead, the
	// IPv4 list would be copied at every step, the old and new arrays both
	// held at once — the largest cost of the whole build.
	ripeReserve4 = 4_500_000
	ripeReserve6 = 1_100_000

	// Reading the IPv4 dump means streaming 223 MB and parsing four million
	// objects as it arrives: seconds on a fast link, many minutes on a slow
	// one. The shared client's limit is sized for files a tenth of that.
	ripeTimeout = 20 * time.Minute

	ripeIndexMagic    = "BRIPEIX1"
	ripeIndexHeader   = 8 + 8 + 8 + 4 + 4 // magic, the two dumps' times, the two counts
	maxRIPEIndexBytes = 256 << 20
)

// RIPE reports the country the RIPE Database records for each address block:
// the registry's own data for its region — Europe, the Middle East and Central
// Asia — rather than any vendor's reading of it. No account, published daily.
//
// It is reported as the registered country, never as the location. RIPE's own
// documentation says of the attribute that "it has never been specified what
// this country represents" — head office, server centre or end user — and
// "therefore, it cannot be used in any reliable way to map IP addresses to
// countries". Set beside MaxMind's registered country it is a second opinion
// on the registration; set beside the geolocation it would read as a
// disagreement about where the address is, for every VPN and hosting range.
//
// The dumps, 223 MB and 38 MB compressed, are never stored. They are streamed
// only when their Last-Modified has moved past the pair the installed index
// was built from, and reduced to what a lookup needs: the country of the
// innermost block over every stretch of the address space. They publish no
// checksum; what guards them is HTTPS, gzip's CRC — a truncated stream is an
// error rather than a short file — and a floor on how many blocks a real dump
// yields.
type RIPE struct {
	dataDir            string
	client             *http.Client
	url4, url6         string
	min4, min6         int
	reserve4, reserve6 int

	dbs swappable
}

func NewRIPE(dataDir string, client *http.Client) *RIPE {
	c := &http.Client{Timeout: ripeTimeout}
	if client != nil {
		copied := *client
		copied.Timeout = ripeTimeout
		c = &copied
	}
	return &RIPE{
		dataDir:  dataDir,
		client:   c,
		url4:     ripeInetnumURL,
		url6:     ripeInet6numURL,
		min4:     ripeMinBlocks4,
		min6:     ripeMinBlocks6,
		reserve4: ripeReserve4,
		reserve6: ripeReserve6,
	}
}

func (p *RIPE) Name() string { return "RIPE" }

func (p *RIPE) Provides() Fields { return FieldRegisteredCountry }

func (p *RIPE) path() string { return filepath.Join(p.dataDir, "ripe-registry.idx") }

func (p *RIPE) FilesPresent() bool { return filesPresent(p.path()) }

func (p *RIPE) Lookup(ip net.IP) Record { return p.dbs.lookup(ip) }

func (p *RIPE) Open() error {
	idx, err := openRIPEIndex(p.path())
	if err != nil {
		return err
	}
	p.dbs.swap(idx)
	return nil
}

func (p *RIPE) Close() { p.dbs.closeAll() }

// Checking costs two HEAD requests; the rebuild happens only once a day, when
// RIPE regenerates the dumps.
func (p *RIPE) MinRefresh() time.Duration { return 0 }

func (p *RIPE) Download() error {
	dest := p.path()
	removeStaleTemps(dest)

	head4, err := p.lastModified(p.url4)
	if err != nil {
		return err
	}
	head6, err := p.lastModified(p.url6)
	if err != nil {
		return err
	}
	if p.current(dest, head4, head6) {
		return errNotModified
	}

	started := time.Now()
	var idx ripeIndexData

	blocks4 := make([]ripeBlock[v4addr], 0, p.reserve4)
	idx.from4, err = p.stream(p.url4, "inetnum", func(key, country []byte) {
		if start, end, ok := parseRange4(key); ok {
			blocks4 = append(blocks4, ripeBlock[v4addr]{start, end, ccOf(country)})
		}
	})
	if err != nil {
		return err
	}
	n4 := len(blocks4)
	if n4 < p.min4 {
		return fmt.Errorf("RIPE: inetnum dump yielded %d blocks, fewer than %d; not installed", n4, p.min4)
	}
	idx.starts4, idx.cc4 = flatten(blocks4)
	blocks4 = nil // released before the IPv6 pass, which peaks separately

	blocks6 := make([]ripeBlock[v6addr], 0, p.reserve6)
	idx.from6, err = p.stream(p.url6, "inet6num", func(key, country []byte) {
		if start, end, ok := parsePrefix6(key); ok {
			blocks6 = append(blocks6, ripeBlock[v6addr]{start, end, ccOf(country)})
		}
	})
	if err != nil {
		return err
	}
	n6 := len(blocks6)
	if n6 < p.min6 {
		return fmt.Errorf("RIPE: inet6num dump yielded %d blocks, fewer than %d; not installed", n6, p.min6)
	}
	idx.starts6, idx.cc6 = flatten(blocks6)

	tmp := dest + ".tmp"
	if err := idx.write(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("RIPE: write index: %w", err)
	}
	check, err := openRIPEIndex(tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("RIPE: built index unreadable: %w", err)
	}
	check.close()
	if err := install([]pendingFile{{tmp: tmp, dest: dest}}); err != nil {
		return err
	}
	log.Printf("RIPE: indexed %d IPv4 and %d IPv6 blocks as %d and %d ranges in %s",
		n4, n6, len(idx.starts4), len(idx.starts6), time.Since(started).Round(time.Second))
	return nil
}

// current reports whether the installed index was built from dumps no older
// than the ones published. It opens the whole index rather than reading the
// stamps alone: a damaged one must be rebuilt, not kept until tomorrow's dump.
func (p *RIPE) current(dest string, head4, head6 time.Time) bool {
	if head4.IsZero() || head6.IsZero() {
		return false
	}
	idx, err := openRIPEIndex(dest)
	if err != nil {
		return false
	}
	defer idx.close()
	return !head4.After(idx.from4) && !head6.After(idx.from6)
}

// lastModified asks for a dump's Last-Modified without fetching it. A server
// that gives none gets a zero time, which always reads as changed.
func (p *RIPE) lastModified(rawURL string) (time.Time, error) {
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return time.Time{}, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return time.Time{}, fmt.Errorf("RIPE: %s: %w", rawURL, sanitizeURLErr(err))
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("RIPE: %s: status %d", rawURL, resp.StatusCode)
	}
	t, err := http.ParseTime(resp.Header.Get("Last-Modified"))
	if err != nil {
		return time.Time{}, nil
	}
	return t, nil
}

// stream reads a gzipped RPSL dump as it arrives and calls fn with each
// object's key and first country, both only valid for the call. It returns the
// dump's Last-Modified, which the index records as what it was built from.
func (p *RIPE) stream(rawURL, class string, fn func(key, country []byte)) (time.Time, error) {
	resp, err := httpGet(p.client, rawURL, "", "")
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	lm, _ := http.ParseTime(resp.Header.Get("Last-Modified"))

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return time.Time{}, fmt.Errorf("RIPE %s: %w", class, err)
	}
	defer gz.Close()
	if err := parseRPSL(gz, class, fn); err != nil {
		return time.Time{}, fmt.Errorf("RIPE %s: %w", class, err)
	}
	return lm, nil
}

// parseRPSL walks RPSL objects — attribute lines, a blank line between objects
// — and reports the key and first country of each object of the given class.
// Comments after "#" are dropped. A read error, including gzip finding the
// stream truncated or corrupt, ends the walk with that error.
func parseRPSL(r io.Reader, class string, fn func(key, country []byte)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	classPrefix := []byte(class + ":")
	countryPrefix := []byte("country:")

	var key, country []byte
	in := false
	flush := func() {
		if in && len(key) > 0 {
			fn(key, country)
		}
		key, country, in = key[:0], country[:0], false
	}
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case len(line) == 0:
			flush()
		case bytes.HasPrefix(line, classPrefix):
			flush()
			in = true
			key = append(key, attrValue(line[len(classPrefix):])...)
		case in && len(country) == 0 && bytes.HasPrefix(line, countryPrefix):
			country = append(country, attrValue(line[len(countryPrefix):])...)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

func attrValue(v []byte) []byte {
	if i := bytes.IndexByte(v, '#'); i >= 0 {
		v = v[:i]
	}
	return bytes.TrimSpace(v)
}

// ccOf keeps a real country code and drops everything else — RIPE's "EU", for
// a block registered to the whole region, included.
func ccOf(country []byte) [2]byte {
	var cc [2]byte
	if c := countryCode(string(country)); c != "" {
		copy(cc[:], c)
	}
	return cc
}

// parseRange4 reads an inetnum key, "192.0.2.0 - 192.0.2.255".
func parseRange4(key []byte) (start, end v4addr, ok bool) {
	a, b, found := bytes.Cut(key, []byte("-"))
	if !found {
		return 0, 0, false
	}
	start, ok1 := parseIPv4(bytes.TrimSpace(a))
	end, ok2 := parseIPv4(bytes.TrimSpace(b))
	if !ok1 || !ok2 || end < start {
		return 0, 0, false
	}
	return start, end, true
}

// parseIPv4 reads a dotted quad as strictly as netip does — four decimal
// octets, no leading zeros — but from bytes, without the string netip would
// need: a dump holds eight million of them.
func parseIPv4(b []byte) (v4addr, bool) {
	var addr uint32
	for i := range 4 {
		if i > 0 {
			if len(b) == 0 || b[0] != '.' {
				return 0, false
			}
			b = b[1:]
		}
		n, digits := 0, 0
		for digits < len(b) && digits < 4 && '0' <= b[digits] && b[digits] <= '9' {
			n = n*10 + int(b[digits]-'0')
			digits++
		}
		if digits == 0 || digits > 3 || n > 255 || digits > 1 && b[0] == '0' {
			return 0, false
		}
		addr = addr<<8 | uint32(n)
		b = b[digits:]
	}
	return v4addr(addr), len(b) == 0
}

// parsePrefix6 reads an inet6num key, "2001:db8::/32".
func parsePrefix6(key []byte) (start, end v6addr, ok bool) {
	pfx, err := netip.ParsePrefix(string(key))
	if err != nil || !pfx.Addr().Is6() || pfx.Addr().Is4In6() {
		return v6addr{}, v6addr{}, false
	}
	pfx = pfx.Masked()
	b := pfx.Addr().As16()
	start = v6addr{be.Uint64(b[:8]), be.Uint64(b[8:])}
	return start, start.lastIn(pfx.Bits()), true
}

// ---------------------------------------------------------------- flattening

// ripeAddr is an address the index can order and step through: 32-bit for
// IPv4 and 128-bit for IPv6, kept apart so four million IPv4 blocks cost four
// bytes an address rather than sixteen.
type ripeAddr[A any] interface {
	comparable
	less(A) bool
	next() (A, bool) // false past the last address
}

type v4addr uint32

func (a v4addr) less(b v4addr) bool { return a < b }

func (a v4addr) next() (v4addr, bool) {
	if a == math.MaxUint32 {
		return 0, false
	}
	return a + 1, true
}

type v6addr struct{ hi, lo uint64 }

func (a v6addr) less(b v6addr) bool { return a.hi < b.hi || a.hi == b.hi && a.lo < b.lo }

func (a v6addr) next() (v6addr, bool) {
	if a.lo != math.MaxUint64 {
		return v6addr{a.hi, a.lo + 1}, true
	}
	if a.hi != math.MaxUint64 {
		return v6addr{a.hi + 1, 0}, true
	}
	return v6addr{}, false
}

// lastIn is the last address of the prefix of the given length starting at a.
func (a v6addr) lastIn(bits int) v6addr {
	switch host := 128 - bits; {
	case host >= 128:
		return v6addr{math.MaxUint64, math.MaxUint64}
	case host >= 64:
		return v6addr{a.hi | (1<<(host-64) - 1), math.MaxUint64}
	default:
		return v6addr{a.hi, a.lo | (1<<host - 1)}
	}
}

type ripeBlock[A ripeAddr[A]] struct {
	start, end A // inclusive
	cc         [2]byte
}

// flatten turns nested blocks into a partition of the address space: sorted
// starts, each beginning a stretch that takes its country from the innermost
// block over it — zero bytes where there is no block, or where the innermost
// one names no single country. The innermost block is the registration for
// those addresses; an "EU" assignment inside a German allocation is not
// German. Adjacent stretches with the same country are merged, which is most
// of the saving: an allocation holding a thousand same-country assignments
// becomes one stretch.
//
// Blocks are ordered by start, outermost first, and swept with a stack of the
// ones still open. RIPE's hierarchy nests blocks properly; one that merely
// overlaps another is treated as the more specific over the overlap, as a
// later, narrower registration would imply. Two blocks with the same range
// cannot both exist, the range being an object's key; were they to, the
// order below still settles it the same way every time.
func flatten[A ripeAddr[A]](blocks []ripeBlock[A]) (starts []A, ccs [][2]byte) {
	slices.SortFunc(blocks, func(x, y ripeBlock[A]) int {
		switch {
		case x.start.less(y.start):
			return -1
		case y.start.less(x.start):
			return 1
		case y.end.less(x.end): // same start: the wider block is the outer one
			return -1
		case x.end.less(y.end):
			return 1
		}
		return bytes.Compare(x.cc[:], y.cc[:])
	})

	var none [2]byte
	emit := func(at A, cc [2]byte) {
		n := len(starts)
		if n > 0 && starts[n-1] == at {
			// Nothing lies between: the later, more specific claim replaces it.
			ccs[n-1] = cc
			if n > 1 && ccs[n-2] == cc {
				starts, ccs = starts[:n-1], ccs[:n-1]
			}
			return
		}
		if n > 0 && ccs[n-1] == cc {
			return // continues the stretch before
		}
		if n == 0 && cc == none {
			return // nothing to record before the first block
		}
		starts = append(starts, at)
		ccs = append(ccs, cc)
	}

	var open []ripeBlock[A]
	// closeUntil ends every open block that ends before limit, or every one.
	closeUntil := func(limit A, all bool) {
		for len(open) > 0 && (all || open[len(open)-1].end.less(limit)) {
			top := open[len(open)-1]
			open = open[:len(open)-1]
			at, more := top.end.next()
			if !more {
				open = open[:0] // the end of the address space
				return
			}
			// Blocks beneath that ended no later than this one are over too.
			for len(open) > 0 && open[len(open)-1].end.less(at) {
				open = open[:len(open)-1]
			}
			if len(open) > 0 {
				emit(at, open[len(open)-1].cc)
			} else {
				emit(at, none)
			}
		}
	}

	for _, b := range blocks {
		closeUntil(b.start, false)
		emit(b.start, b.cc)
		open = append(open, b)
	}
	var zero A
	closeUntil(zero, true)
	return starts, ccs
}

// --------------------------------------------------------------------- index

// ripeIndexData is a built index, before it is written. On disk it is the
// magic, the Last-Modified of the two dumps it was built from and the two
// counts, then for IPv4 the starts (4 bytes each) and their countries, then
// the same for IPv6 (16-byte starts). Everything is big-endian.
type ripeIndexData struct {
	from4, from6 time.Time
	starts4      []v4addr
	cc4          [][2]byte
	starts6      []v6addr
	cc6          [][2]byte
}

func (d *ripeIndexData) write(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)

	var hdr [ripeIndexHeader]byte
	copy(hdr[:], ripeIndexMagic)
	be.PutUint64(hdr[8:], unixOrZero(d.from4))
	be.PutUint64(hdr[16:], unixOrZero(d.from6))
	be.PutUint32(hdr[24:], uint32(len(d.starts4)))
	be.PutUint32(hdr[28:], uint32(len(d.starts6)))
	w.Write(hdr[:])

	var b [16]byte
	for _, s := range d.starts4 {
		be.PutUint32(b[:4], uint32(s))
		w.Write(b[:4])
	}
	for _, cc := range d.cc4 {
		w.Write(cc[:])
	}
	for _, s := range d.starts6 {
		be.PutUint64(b[:8], s.hi)
		be.PutUint64(b[8:], s.lo)
		w.Write(b[:])
	}
	for _, cc := range d.cc6 {
		w.Write(cc[:])
	}

	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func unixOrZero(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.Unix())
}

func timeOrZero(v uint64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(int64(v), 0).UTC()
}

// ripeIndex is an opened index: views into a memory-mapped file, so the same
// rule applies as for locDB — nothing is read after release, and a lookup
// copies out what it returns.
type ripeIndex struct {
	release func() error

	from4, from6 time.Time
	n4, n6       int
	starts4, cc4 []byte
	starts6, cc6 []byte
}

func openRIPEIndex(path string) (*ripeIndex, error) {
	data, release, err := mapFile(path, maxRIPEIndexBytes)
	if err != nil {
		return nil, err
	}
	idx, err := parseRIPEIndex(data)
	if err != nil {
		release()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	idx.release = release
	return idx, nil
}

func parseRIPEIndex(data []byte) (*ripeIndex, error) {
	if len(data) < ripeIndexHeader || string(data[:len(ripeIndexMagic)]) != ripeIndexMagic {
		return nil, errors.New("not a RIPE index")
	}
	idx := &ripeIndex{
		from4: timeOrZero(be.Uint64(data[8:])),
		from6: timeOrZero(be.Uint64(data[16:])),
		n4:    int(be.Uint32(data[24:])),
		n6:    int(be.Uint32(data[28:])),
	}
	want := uint64(ripeIndexHeader) + uint64(idx.n4)*6 + uint64(idx.n6)*18
	if uint64(len(data)) != want {
		return nil, fmt.Errorf("RIPE index is %d bytes, its counts say %d", len(data), want)
	}
	off := ripeIndexHeader
	take := func(n int) []byte { s := data[off : off+n]; off += n; return s }
	idx.starts4 = take(idx.n4 * 4)
	idx.cc4 = take(idx.n4 * 2)
	idx.starts6 = take(idx.n6 * 16)
	idx.cc6 = take(idx.n6 * 2)
	return idx, nil
}

// lookup finds the stretch holding the address: the last start at or below it.
func (x *ripeIndex) lookup(ip net.IP) Record {
	var rec Record
	if x == nil {
		return rec
	}
	var cc []byte
	if v4 := ip.To4(); v4 != nil {
		a := be.Uint32(v4)
		i := sort.Search(x.n4, func(i int) bool { return be.Uint32(x.starts4[i*4:]) > a }) - 1
		if i < 0 {
			return rec
		}
		cc = x.cc4[i*2 : i*2+2]
	} else if v6 := ip.To16(); v6 != nil {
		hi, lo := be.Uint64(v6[:8]), be.Uint64(v6[8:])
		i := sort.Search(x.n6, func(i int) bool {
			s := x.starts6[i*16:]
			shi, slo := be.Uint64(s), be.Uint64(s[8:])
			return shi > hi || shi == hi && slo > lo
		}) - 1
		if i < 0 {
			return rec
		}
		cc = x.cc6[i*2 : i*2+2]
	}
	// string() copies out of the mapping, which it must: the result outlives
	// the read lock.
	if c := countryCode(string(cc)); c != "" {
		rec.RegisteredCountryCode = c
		rec.HasData = true
	}
	return rec
}

func (x *ripeIndex) close() {
	if x != nil && x.release != nil {
		x.release()
		x.release = nil
	}
}
