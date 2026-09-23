package geoip

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"
)

// This file reads IPFire's location database, the format libloc writes: version
// 1, laid out in libloc's src/libloc/format.h and read by src/database.c. There
// is no Go library for it and no MMDB export, and the format is small enough to
// read directly.
//
// Every integer is big-endian. After an eight-byte magic comes a fixed header
// of section offsets and two signature slots, then five sections: AS records,
// the network tree, network records, country records, and a pool of
// NUL-terminated strings the records point into. Addresses are 128-bit, with
// IPv4 held as IPv4-mapped IPv6 (::ffff:a.b.c.d), exactly as net.IP.To16
// produces it.

const (
	locMagic     = "LOCDBXX"
	locVersion1  = 1
	locMagicSize = 8 // struct loc_database_magic: char[7] + uint8 version

	// struct loc_database_header_v1: nine uint32 pairs and fields before the
	// signatures, two 2048-byte signature slots, 32 bytes of padding.
	locHeaderSize = 4192
	locSigMax     = 2048

	locSig1LenAt = 60
	locSig2LenAt = 62
	locSig1At    = 64
	locSig2At    = locSig1At + locSigMax

	locNodeSize    = 12 // zero, one, network: uint32 each
	locNetworkSize = 12 // country_code[2], 2 bytes padding, asn uint32, flags uint16, 2 bytes padding
	locASSize      = 8  // number, name offset
	locCountrySize = 8  // code[2], continent_code[2], name offset

	// A node whose network field holds this is not a leaf.
	locNoNetwork = 0xffffffff
)

// Network flags, from enum loc_network_flags in src/libloc/network.h.
const (
	locFlagAnonymousProxy    = 1 << 0
	locFlagSatelliteProvider = 1 << 1
	locFlagAnycast           = 1 << 2
	// 1 << 3 is "drop": hostile networks from the Spamhaus DROP family of
	// lists. beacon's schema has nowhere to say that yet, so it is not read.
)

var be = binary.BigEndian

// locDB is one opened database. Everything it holds is a view into data, which
// is memory-mapped: nothing may be read from it after release, and anything a
// lookup returns is copied out first.
type locDB struct {
	data    []byte
	release func() error

	createdAt time.Time
	ases      []byte
	nodes     []byte
	networks  []byte
	countries []byte
	pool      []byte
	sig1      []byte
	sig2      []byte
}

func openLocDB(path string) (*locDB, error) {
	data, release, err := mapFile(path, maxLocDBBytes)
	if err != nil {
		return nil, err
	}
	db, err := parseLocDB(data)
	if err != nil {
		release()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	db.release = release
	return db, nil
}

// parseLocDB checks the header and that every section lies inside the file and
// is a whole number of records, which is what lets lookups index without
// checking each read.
func parseLocDB(data []byte) (*locDB, error) {
	if len(data) < locMagicSize+locHeaderSize {
		return nil, errors.New("too short for a location database")
	}
	if string(data[:len(locMagic)]) != locMagic {
		return nil, errors.New("not a location database")
	}
	if v := data[len(locMagic)]; v != locVersion1 {
		return nil, fmt.Errorf("unsupported location database version %d", v)
	}

	h := data[locMagicSize : locMagicSize+locHeaderSize]
	db := &locDB{
		data:      data,
		createdAt: time.Unix(int64(be.Uint64(h[0:])), 0).UTC(),
	}

	section := func(name string, at, recordSize int) ([]byte, error) {
		off, n := uint64(be.Uint32(h[at:])), uint64(be.Uint32(h[at+4:]))
		if off+n > uint64(len(data)) {
			return nil, fmt.Errorf("%s section runs past the end of the file", name)
		}
		if n%uint64(recordSize) != 0 {
			return nil, fmt.Errorf("%s section is not a whole number of records", name)
		}
		return data[off : off+n], nil
	}
	var err error
	if db.ases, err = section("AS", 20, locASSize); err != nil {
		return nil, err
	}
	if db.networks, err = section("network", 28, locNetworkSize); err != nil {
		return nil, err
	}
	if db.nodes, err = section("network tree", 36, locNodeSize); err != nil {
		return nil, err
	}
	if db.countries, err = section("country", 44, locCountrySize); err != nil {
		return nil, err
	}
	if db.pool, err = section("string pool", 52, 1); err != nil {
		return nil, err
	}
	if len(db.nodes) == 0 {
		return nil, errors.New("network tree is empty")
	}

	l1, l2 := int(be.Uint16(h[locSig1LenAt:])), int(be.Uint16(h[locSig2LenAt:]))
	if l1 > locSigMax || l2 > locSigMax {
		return nil, errors.New("signature longer than its slot")
	}
	db.sig1 = h[locSig1At : locSig1At+l1]
	db.sig2 = h[locSig2At : locSig2At+l2]
	return db, nil
}

// verify checks the database against key. As loc_database_verify computes it,
// the signed message is the magic, then the header with both signatures and
// their lengths zeroed, then every byte after the header, hashed with SHA-256
// (OpenSSL's default digest for an EC key). Either signature slot may hold a
// valid signature; the second exists so the key can be rotated.
func (db *locDB) verify(key *ecdsa.PublicKey) error {
	if len(db.sig1) == 0 && len(db.sig2) == 0 {
		return errors.New("database is not signed")
	}

	var header [locHeaderSize]byte
	copy(header[:], db.data[locMagicSize:])
	clear(header[locSig1LenAt : locSig2At+locSigMax])

	h := sha256.New()
	h.Write(db.data[:locMagicSize])
	h.Write(header[:])
	h.Write(db.data[locMagicSize+locHeaderSize:])
	sum := h.Sum(nil)

	for _, sig := range [][]byte{db.sig1, db.sig2} {
		if len(sig) > 0 && ecdsa.VerifyASN1(key, sum, sig) {
			return nil
		}
	}
	return errors.New("signature does not verify")
}

// lookup walks the tree along the address's bits and answers from the deepest
// node that carries a network: the most specific prefix containing the address,
// which is what __loc_database_lookup returns.
func (db *locDB) lookup(ip net.IP) Record {
	var rec Record
	if db == nil {
		return rec
	}
	addr := ip.To16()
	if addr == nil {
		return rec
	}

	nodeCount := uint32(len(db.nodes) / locNodeSize)
	node, found := uint32(0), uint32(locNoNetwork)
	for depth := 0; ; depth++ {
		n := db.nodes[node*locNodeSize : (node+1)*locNodeSize]
		if leaf := be.Uint32(n[8:]); leaf != locNoNetwork {
			found = leaf
		}
		if depth == 8*net.IPv6len {
			break
		}
		bit := addr[depth/8] >> (7 - depth%8) & 1
		next := be.Uint32(n[bit*4:])
		// Zero is the root, so it never appears as a child: it ends the path.
		if next == 0 || next >= nodeCount {
			break
		}
		node = next
	}
	if found == locNoNetwork || found >= uint32(len(db.networks)/locNetworkSize) {
		return rec
	}

	e := db.networks[found*locNetworkSize : (found+1)*locNetworkSize]
	if cc := countryCode(string(e[0:2])); cc != "" {
		rec.CountryCode = cc
		if name, continent, ok := db.country(cc); ok {
			rec.Country = name
			if cname, known := continentNames[continent]; known {
				rec.ContinentCode = continent
				rec.Continent = cname
			}
		}
		rec.HasData = true
	}
	if asn := be.Uint32(e[4:8]); asn != 0 {
		rec.ASN = uint(asn)
		rec.ASNOrg = db.asName(asn)
		rec.HasData = true
	}

	flags := be.Uint16(e[8:10])
	rec.IsAnonymousProxy = flags&locFlagAnonymousProxy != 0
	rec.IsSatelliteProvider = flags&locFlagSatelliteProvider != 0
	rec.IsAnycast = flags&locFlagAnycast != 0
	if rec.IsAnonymousProxy || rec.IsSatelliteProvider || rec.IsAnycast {
		rec.HasData = true
	}
	return rec
}

// country finds a country record by binary search; the section is sorted by
// code, as loc_database_get_country relies on.
func (db *locDB) country(code string) (name, continent string, ok bool) {
	n := len(db.countries) / locCountrySize
	i := sort.Search(n, func(i int) bool {
		return string(db.countries[i*locCountrySize:i*locCountrySize+2]) >= code
	})
	if i == n {
		return "", "", false
	}
	c := db.countries[i*locCountrySize : (i+1)*locCountrySize]
	if string(c[0:2]) != code {
		return "", "", false
	}
	return db.str(be.Uint32(c[4:8])), string(c[2:4]), true
}

// asName finds an AS record by binary search; the section is sorted by number,
// as loc_database_get_as relies on.
func (db *locDB) asName(number uint32) string {
	n := len(db.ases) / locASSize
	i := sort.Search(n, func(i int) bool {
		return be.Uint32(db.ases[i*locASSize:]) >= number
	})
	if i == n || be.Uint32(db.ases[i*locASSize:]) != number {
		return ""
	}
	return db.str(be.Uint32(db.ases[i*locASSize+4:]))
}

// str reads a NUL-terminated string from the pool. The conversion copies it
// out of the mapping, which it must: the result outlives the read lock.
func (db *locDB) str(off uint32) string {
	if uint64(off) >= uint64(len(db.pool)) {
		return ""
	}
	s := db.pool[off:]
	if end := bytes.IndexByte(s, 0); end >= 0 {
		s = s[:end]
	}
	return string(s)
}

func (db *locDB) close() {
	if db != nil && db.release != nil {
		db.release()
		db.release = nil
	}
}
