// Package tlsfp computes TLS client fingerprints from a ClientHello.
//
// Everything here comes from the standard library's tls.ClientHelloInfo, which
// exposes the cipher suites, the extension IDs in wire order, the supported
// groups, the signature algorithms and the offered ALPN protocols exactly as
// the client sent them, GREASE included. No third-party TLS stack and no raw
// ClientHello capture is needed.
//
// This only works because beacon terminates TLS itself. A proxy that
// terminates TLS consumes the ClientHello and it cannot be recovered
// downstream, which is why the fingerprints are absent in plain-HTTP mode.
//
// JA3 is Salesforce's, JA4 is FoxIO's and is BSD-3-Clause licensed; the rest
// of the JA4+ suite is not, and none of it is implemented here.
package tlsfp

import (
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Fingerprint is one client's TLS identity, in the shapes people compare.
type Fingerprint struct {
	JA3      string // the classic string, extensions in wire order
	JA3Hash  string
	JA3N     string // extensions sorted, defeating extension permutation
	JA3NHash string

	JA4   string // sorted, hashed
	JA4R  string // sorted, unhashed
	JA4O  string // wire order, hashed
	JA4RO string // wire order, unhashed

	// The decoded ClientHello, for callers that want to see the parts rather
	// than a digest.
	TLSVersion     string
	CipherSuites   []uint16
	Extensions     []uint16
	SupportedTLS   []uint16
	Curves         []uint16
	PointFormats   []uint8
	SignatureAlgos []uint16
	ALPN           []string
	ServerName     string
	GREASE         bool
}

// isGREASE reports the reserved values clients inject to keep middleboxes
// honest (RFC 8701). They are random per connection, so every fingerprint
// format excludes them.
func isGREASE(v uint16) bool {
	return v&0x0f0f == 0x0a0a && byte(v>>8) == byte(v)
}

func withoutGREASE(in []uint16) []uint16 {
	out := make([]uint16, 0, len(in))
	for _, v := range in {
		if !isGREASE(v) {
			out = append(out, v)
		}
	}
	return out
}

func hasGREASE(lists ...[]uint16) bool {
	for _, l := range lists {
		for _, v := range l {
			if isGREASE(v) {
				return true
			}
		}
	}
	return false
}

func contains(list []uint16, want uint16) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func joinDecimal(vals []uint16, sep string) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, strconv.FormatUint(uint64(v), 10))
	}
	return strings.Join(parts, sep)
}

func joinHex(vals []uint16) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprintf("%04x", v))
	}
	return strings.Join(parts, ",")
}

func sortedCopy(in []uint16) []uint16 {
	out := append([]uint16(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// truncatedSHA256 is JA4's digest: the first 12 hex characters of SHA-256, or
// twelve zeroes when there is nothing to hash.
func truncatedSHA256(s string) string {
	if s == "" {
		return "000000000000"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

const (
	extServerName        uint16 = 0x0000
	extALPN              uint16 = 0x0010
	extSupportedVersions uint16 = 0x002b
)

// legacyVersion is JA3's first field: the ClientHello's legacy_version, which
// the standard library does not expose directly.
//
// RFC 8446 §4.1.2 requires it to be 0x0303 for any client sending
// supported_versions, which is every TLS 1.3-capable client. Without that
// extension the library derives SupportedVersions from legacy_version, so its
// highest entry is the value itself.
func legacyVersion(chi *tls.ClientHelloInfo) uint16 {
	if contains(chi.Extensions, extSupportedVersions) {
		return tls.VersionTLS12
	}
	var max uint16
	for _, v := range chi.SupportedVersions {
		if !isGREASE(v) && v > max {
			max = v
		}
	}
	if max == 0 {
		return tls.VersionTLS12
	}
	return max
}

// negotiableVersion is the highest real version the client offered, which is
// what JA4 names.
func negotiableVersion(chi *tls.ClientHelloInfo) uint16 {
	var max uint16
	for _, v := range chi.SupportedVersions {
		if !isGREASE(v) && v > max {
			max = v
		}
	}
	return max
}

func versionLabel(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "13"
	case tls.VersionTLS12:
		return "12"
	case tls.VersionTLS11:
		return "11"
	case tls.VersionTLS10:
		return "10"
	case 0x0300:
		return "s3"
	case 0x0200:
		return "s2"
	default:
		return "00"
	}
}

func versionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	case 0x0300:
		return "SSL 3.0"
	default:
		return "unknown"
	}
}

// alpnCode is JA4's two-character ALPN field: the first and last character of
// the first offered protocol, "00" when none was offered.
func alpnCode(protos []string) string {
	if len(protos) == 0 || protos[0] == "" {
		return "00"
	}
	p := protos[0]
	first, last := p[0], p[len(p)-1]
	// Non-printable values are rendered as hex, per the JA4 specification.
	if first < 0x21 || first > 0x7e || last < 0x21 || last > 0x7e {
		return fmt.Sprintf("%x%x", first>>4, last&0x0f)
	}
	return string([]byte{first, last})
}

func twoDigits(n int) string {
	if n > 99 {
		n = 99
	}
	return fmt.Sprintf("%02d", n)
}

// New computes every supported fingerprint from one ClientHello.
func New(chi *tls.ClientHelloInfo) *Fingerprint {
	curves := make([]uint16, 0, len(chi.SupportedCurves))
	for _, c := range chi.SupportedCurves {
		curves = append(curves, uint16(c))
	}
	sigs := make([]uint16, 0, len(chi.SignatureSchemes))
	for _, s := range chi.SignatureSchemes {
		sigs = append(sigs, uint16(s))
	}

	fp := &Fingerprint{
		CipherSuites:   chi.CipherSuites,
		Extensions:     chi.Extensions,
		SupportedTLS:   chi.SupportedVersions,
		Curves:         curves,
		PointFormats:   chi.SupportedPoints,
		SignatureAlgos: sigs,
		ALPN:           chi.SupportedProtos,
		ServerName:     chi.ServerName,
		GREASE:         hasGREASE(chi.CipherSuites, chi.Extensions, curves),
	}
	fp.TLSVersion = versionName(negotiableVersion(chi))

	ciphers := withoutGREASE(chi.CipherSuites)
	exts := withoutGREASE(chi.Extensions)
	cleanCurves := withoutGREASE(curves)
	cleanSigs := withoutGREASE(sigs)

	fp.ja3(chi, ciphers, exts, cleanCurves)
	fp.ja4(chi, ciphers, exts, cleanSigs)
	return fp
}

// ja3 builds "version,ciphers,extensions,curves,pointFormats" and its
// extension-sorted variant.
func (fp *Fingerprint) ja3(chi *tls.ClientHelloInfo, ciphers, exts, curves []uint16) {
	points := make([]string, 0, len(chi.SupportedPoints))
	for _, p := range chi.SupportedPoints {
		points = append(points, strconv.FormatUint(uint64(p), 10))
	}

	field := func(extensions []uint16) string {
		return strings.Join([]string{
			strconv.FormatUint(uint64(legacyVersion(chi)), 10),
			joinDecimal(ciphers, "-"),
			joinDecimal(extensions, "-"),
			joinDecimal(curves, "-"),
			strings.Join(points, "-"),
		}, ",")
	}

	fp.JA3 = field(exts)
	fp.JA3N = field(sortedCopy(exts))

	sum := md5.Sum([]byte(fp.JA3))
	fp.JA3Hash = hex.EncodeToString(sum[:])
	sum = md5.Sum([]byte(fp.JA3N))
	fp.JA3NHash = hex.EncodeToString(sum[:])
}

// ja4 builds the four JA4 variants.
//
// The prefix counts every extension the client sent, but the hashed list omits
// server_name and ALPN — they are already represented in the prefix, as the
// d/i flag and the two-character protocol code. The wire-order variants keep
// them, which is what distinguishes JA4_o from JA4.
func (fp *Fingerprint) ja4(chi *tls.ClientHelloInfo, ciphers, exts, sigs []uint16) {
	prefix := "t" +
		versionLabel(negotiableVersion(chi)) +
		map[bool]string{true: "d", false: "i"}[chi.ServerName != ""] +
		twoDigits(len(ciphers)) +
		twoDigits(len(exts)) +
		alpnCode(chi.SupportedProtos)

	hashed := make([]uint16, 0, len(exts))
	for _, e := range exts {
		if e == extServerName || e == extALPN {
			continue
		}
		hashed = append(hashed, e)
	}

	sigPart := joinHex(sigs)

	sortedCiphers := joinHex(sortedCopy(ciphers))
	sortedExts := joinHex(sortedCopy(hashed))
	if sigPart != "" {
		sortedExts += "_" + sigPart
	}

	wireCiphers := joinHex(ciphers)
	wireExts := joinHex(exts)
	if sigPart != "" {
		wireExts += "_" + sigPart
	}

	fp.JA4R = prefix + "_" + sortedCiphers + "_" + sortedExts
	fp.JA4 = prefix + "_" + truncatedSHA256(sortedCiphers) + "_" + truncatedSHA256(sortedExts)
	fp.JA4RO = prefix + "_" + wireCiphers + "_" + wireExts
	fp.JA4O = prefix + "_" + truncatedSHA256(wireCiphers) + "_" + truncatedSHA256(wireExts)
}
