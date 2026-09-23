package geoip

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

// locTestDB describes a location database to build in libloc's version 1
// layout, so the reader is tested against the format rather than against a
// 50 MB download.
type locTestDB struct {
	nets      []locTestNet
	ases      map[uint32]string
	countries []locTestCountry
	created   time.Time
}

type locTestNet struct {
	prefix  string
	country string // two characters, "" for none
	asn     uint32
	flags   uint16
}

type locTestCountry struct{ code, continent, name string }

// build lays the database out as libloc's writer does: magic, header, then the
// AS, network, tree, country and string-pool sections. With a key it signs the
// result into the given slot (1 or 2), over the message loc_database_verify
// checks.
func (d locTestDB) build(t *testing.T, key *ecdsa.PrivateKey, slot int) []byte {
	t.Helper()

	pool := []byte{0}
	str := func(s string) uint32 {
		off := uint32(len(pool))
		pool = append(append(pool, s...), 0)
		return off
	}
	vendor, desc, license := str("beacon tests"), str("synthetic"), str("CC BY-SA 4.0")

	numbers := make([]uint32, 0, len(d.ases))
	for n := range d.ases {
		numbers = append(numbers, n)
	}
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
	var ases []byte
	for _, n := range numbers {
		ases = be.AppendUint32(ases, n)
		ases = be.AppendUint32(ases, str(d.ases[n]))
	}

	cs := append([]locTestCountry(nil), d.countries...)
	sort.Slice(cs, func(i, j int) bool { return cs[i].code < cs[j].code })
	var countries []byte
	for _, c := range cs {
		countries = append(countries, c.code[0], c.code[1], c.continent[0], c.continent[1])
		countries = be.AppendUint32(countries, str(c.name))
	}

	type node struct{ zero, one, network uint32 }
	nodes := []node{{network: locNoNetwork}}
	var networks []byte
	for i, n := range d.nets {
		p := netip.MustParsePrefix(n.prefix).Masked()
		addr := p.Addr().As16()
		bits := p.Bits()
		if p.Addr().Is4() {
			bits += 96 // held as ::ffff:a.b.c.d
		}
		cur := 0
		for depth := 0; depth < bits; depth++ {
			bit := addr[depth/8] >> (7 - depth%8) & 1
			next := nodes[cur].zero
			if bit == 1 {
				next = nodes[cur].one
			}
			if next == 0 {
				nodes = append(nodes, node{network: locNoNetwork})
				next = uint32(len(nodes) - 1)
				if bit == 0 {
					nodes[cur].zero = next
				} else {
					nodes[cur].one = next
				}
			}
			cur = int(next)
		}
		nodes[cur].network = uint32(i)

		cc := [2]byte{}
		copy(cc[:], n.country)
		networks = append(networks, cc[0], cc[1], 0, 0)
		networks = be.AppendUint32(networks, n.asn)
		networks = be.AppendUint16(networks, n.flags)
		networks = append(networks, 0, 0)
	}
	var tree []byte
	for _, n := range nodes {
		tree = be.AppendUint32(tree, n.zero)
		tree = be.AppendUint32(tree, n.one)
		tree = be.AppendUint32(tree, n.network)
	}

	header := make([]byte, locHeaderSize)
	be.PutUint64(header[0:], uint64(d.created.Unix()))
	be.PutUint32(header[8:], vendor)
	be.PutUint32(header[12:], desc)
	be.PutUint32(header[16:], license)

	var body []byte
	for _, s := range []struct {
		at   int
		data []byte
	}{{20, ases}, {28, networks}, {36, tree}, {44, countries}, {52, pool}} {
		be.PutUint32(header[s.at:], uint32(locMagicSize+locHeaderSize+len(body)))
		be.PutUint32(header[s.at+4:], uint32(len(s.data)))
		body = append(body, s.data...)
	}

	data := append([]byte(locMagic), locVersion1)
	data = append(data, header...)
	data = append(data, body...)

	if key != nil {
		// The signature fields are still zero, which is the form that is signed.
		sum := sha256.Sum256(data)
		sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		lenAt, at := locSig1LenAt, locSig1At
		if slot == 2 {
			lenAt, at = locSig2LenAt, locSig2At
		}
		be.PutUint16(data[locMagicSize+lenAt:], uint16(len(sig)))
		copy(data[locMagicSize+at:], sig)
	}
	return data
}

func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

var sampleLocDB = locTestDB{
	nets: []locTestNet{
		{"10.0.0.0/8", "US", 64500, locFlagAnycast},
		{"10.1.0.0/16", "DE", 64501, 0},
		{"10.2.0.0/16", "EU", 64502, 0},                     // a region, not a country
		{"10.3.0.0/16", "", 0, locFlagSatelliteProvider},    // a flag and nothing else
		{"10.4.0.0/16", "AU", 64503, locFlagAnonymousProxy}, // AS with no name
		{"10.5.0.0/16", "DE", 64501, 1 << 3},                // "drop": not surfaced
		{"2001:db8::/32", "FR", 64504, 0},
	},
	ases: map[uint32]string{
		64500: "Example Anycast",
		64501: "Example Deutschland",
		64502: "Example Europe",
		64503: "",
		64504: "Example France",
	},
	countries: []locTestCountry{
		{"US", "NA", "United States of America"},
		{"DE", "EU", "Germany"},
		{"FR", "EU", "France"},
		{"AU", "OC", "Australia"},
	},
	created: time.Date(2026, 9, 23, 4, 35, 31, 0, time.UTC),
}

func mustParseLocDB(t *testing.T, data []byte) *locDB {
	t.Helper()
	db, err := parseLocDB(data)
	if err != nil {
		t.Fatalf("parseLocDB: %v", err)
	}
	return db
}

func TestLocDBLookup(t *testing.T) {
	db := mustParseLocDB(t, sampleLocDB.build(t, nil, 0))
	if !db.createdAt.Equal(sampleLocDB.created) {
		t.Errorf("createdAt = %v, want %v", db.createdAt, sampleLocDB.created)
	}

	for _, tc := range []struct {
		ip   string
		want Record
	}{
		// The most specific network wins over the /8 that also contains it.
		{"10.1.2.3", Record{
			CountryCode: "DE", Country: "Germany", ContinentCode: "EU", Continent: "Europe",
			ASN: 64501, ASNOrg: "Example Deutschland", HasData: true,
		}},
		{"10.200.0.1", Record{
			CountryCode: "US", Country: "United States of America", ContinentCode: "NA",
			Continent: "North America", ASN: 64500, ASNOrg: "Example Anycast",
			IsAnycast: true, HasData: true,
		}},
		{"10.2.0.1", Record{ASN: 64502, ASNOrg: "Example Europe", HasData: true}},
		{"10.3.0.1", Record{IsSatelliteProvider: true, HasData: true}},
		{"10.4.0.1", Record{
			CountryCode: "AU", Country: "Australia", ContinentCode: "OC", Continent: "Oceania",
			ASN: 64503, IsAnonymousProxy: true, HasData: true,
		}},
		{"10.5.0.1", Record{
			CountryCode: "DE", Country: "Germany", ContinentCode: "EU", Continent: "Europe",
			ASN: 64501, ASNOrg: "Example Deutschland", HasData: true,
		}},
		{"2001:db8::1", Record{
			CountryCode: "FR", Country: "France", ContinentCode: "EU", Continent: "Europe",
			ASN: 64504, ASNOrg: "Example France", HasData: true,
		}},
		{"11.0.0.1", Record{}},
		{"2001:db9::1", Record{}},
	} {
		got := db.lookup(net.ParseIP(tc.ip))
		if got.City != "" || got.Subdivisions != nil {
			t.Errorf("%s: a country-level source returned place data: %+v", tc.ip, got)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.ip, got, tc.want)
		}
	}

	if rec := db.lookup(nil); rec.HasData {
		t.Errorf("lookup(nil) = %+v, want nothing", rec)
	}
}

// IPFire's data carries regional and unknown codes where a country belongs.
func TestCountryCodeRejectsPlaceholders(t *testing.T) {
	for _, cc := range []string{"EU", "AP", "ZZ", "XX", "A1", "O1", "\x00\x00", "", "U", "USA", "1A"} {
		if got := countryCode(cc); got != "" {
			t.Errorf("countryCode(%q) = %q, want none", cc, got)
		}
	}
	for cc, want := range map[string]string{"US": "US", "de": "DE", "XK": "XK"} {
		if got := countryCode(cc); got != want {
			t.Errorf("countryCode(%q) = %q, want %q", cc, got, want)
		}
	}
}

func TestLocDBVerify(t *testing.T) {
	key := testKey(t)

	if err := mustParseLocDB(t, sampleLocDB.build(t, key, 1)).verify(&key.PublicKey); err != nil {
		t.Errorf("slot 1: %v", err)
	}
	// The second slot exists for key rotation and is accepted on its own.
	if err := mustParseLocDB(t, sampleLocDB.build(t, key, 2)).verify(&key.PublicKey); err != nil {
		t.Errorf("slot 2: %v", err)
	}

	other := testKey(t)
	if err := mustParseLocDB(t, sampleLocDB.build(t, key, 1)).verify(&other.PublicKey); err == nil {
		t.Error("verified against the wrong key")
	}

	tampered := sampleLocDB.build(t, key, 1)
	tampered[len(tampered)-2] ^= 0xFF // inside the string pool
	if err := mustParseLocDB(t, tampered).verify(&key.PublicKey); err == nil {
		t.Error("verified a database modified after signing")
	}

	// The header is signed too, apart from the signature fields.
	tampered = sampleLocDB.build(t, key, 1)
	tampered[locMagicSize] ^= 0x01 // created_at
	if err := mustParseLocDB(t, tampered).verify(&key.PublicKey); err == nil {
		t.Error("verified a database whose header was modified after signing")
	}

	if err := mustParseLocDB(t, sampleLocDB.build(t, nil, 0)).verify(&key.PublicKey); err == nil {
		t.Error("verified an unsigned database")
	}
}

func TestIPFireSigningKeyParses(t *testing.T) {
	key := mustParseECKey(ipfireSigningKey)
	if key.Curve != elliptic.P521() {
		t.Errorf("IPFire's key is on %s, want P-521", key.Curve.Params().Name)
	}
}

// A malformed file must be refused at open, never become a panic mid-lookup.
func TestParseLocDBRejectsMalformed(t *testing.T) {
	good := sampleLocDB.build(t, nil, 0)
	header := func(mutate func(h []byte)) []byte {
		b := append([]byte(nil), good...)
		mutate(b[locMagicSize : locMagicSize+locHeaderSize])
		return b
	}

	for name, data := range map[string][]byte{
		"empty":        nil,
		"short header": good[:locMagicSize+100],
		"bad magic":    append([]byte("LOCDBYY\x01"), good[locMagicSize:]...),
		"version 2":    append([]byte("LOCDBXX\x02"), good[locMagicSize:]...),
		"pool past end": header(func(h []byte) {
			be.PutUint32(h[56:], uint32(len(good)))
		}),
		"partial network record": header(func(h []byte) {
			be.PutUint32(h[32:], be.Uint32(h[32:])-1)
		}),
		"empty tree": header(func(h []byte) {
			be.PutUint32(h[40:], 0)
		}),
		"signature longer than its slot": header(func(h []byte) {
			be.PutUint16(h[locSig1LenAt:], locSigMax+1)
		}),
	} {
		if _, err := parseLocDB(data); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

// A child index past the end of the tree ends the walk instead of reading
// outside it.
func TestLocDBLookupStopsAtBadChild(t *testing.T) {
	data := sampleLocDB.build(t, nil, 0)
	db := mustParseLocDB(t, data)
	be.PutUint32(db.nodes[0:], 0xFFFFFFF0) // root's zero child
	be.PutUint32(db.nodes[4:], 0xFFFFFFF0) // and its one child

	if rec := db.lookup(net.ParseIP("10.1.2.3")); rec.HasData {
		t.Errorf("lookup through a corrupt tree returned %+v", rec)
	}
}

func TestOpenLocDBMapsAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "location.db")
	if err := os.WriteFile(path, sampleLocDB.build(t, nil, 0), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := openLocDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec := db.lookup(net.ParseIP("2001:db8::1")); rec.CountryCode != "FR" {
		t.Errorf("lookup = %+v, want FR", rec)
	}
	db.close()
	db.close() // idempotent

	if err := os.WriteFile(path, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocDB(path); err == nil {
		t.Error("opened a file that is not a location database")
	}
}
