package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	// The zone database, compiled in. The runtime image has none at
	// /usr/share/zoneinfo, so without this every time.LoadLocation failed there
	// and local_time was null for every source that reports a time zone — which
	// went unseen while DB-IP, which reports none, was the only source.
	_ "time/tzdata"

	"beacon/internal/geoip"
	"beacon/internal/h2fp"
	"beacon/internal/tlsfp"
)

// SchemaVersion is the shape returned when no explicit version is requested.
// Version 1 is the original flat object and is kept for existing clients.
const SchemaVersion = 2

type Response struct {
	IP       string
	Hostname string
	Answers  []geoip.Answer

	// TLS is nil unless the request arrived over a connection beacon
	// terminated itself. Behind a TLS-terminating proxy the ClientHello is
	// consumed upstream and cannot be recovered.
	TLS        *tlsfp.Fingerprint
	Negotiated *NegotiatedTLS

	// HTTP2 is nil unless the request arrived over HTTP/2.
	HTTP2 *h2fp.Fingerprint
}

// NegotiatedTLS is what the two sides actually settled on, as opposed to what
// the client offered.
type NegotiatedTLS struct {
	Version     string
	CipherSuite string
	CurveID     string
	ALPN        string
	ServerName  string
	Resumed     bool
	ECHAccepted bool
}

func New(ip, hostname string, answers []geoip.Answer) Response {
	return Response{IP: ip, Hostname: hostname, Answers: answers}
}

// WithTLS attaches the fingerprint and negotiated parameters for this request.
func (resp Response) WithTLS(fp *tlsfp.Fingerprint, n *NegotiatedTLS) Response {
	resp.TLS, resp.Negotiated = fp, n
	return resp
}

// WithHTTP2 attaches the Akamai HTTP/2 fingerprint for this request.
func (resp Response) WithHTTP2(fp *h2fp.Fingerprint) Response {
	resp.HTTP2 = fp
	return resp
}

// primary merges the sources field by field, taking each value from the first
// source that has one.
//
// Per-field rather than "the first source that had anything": MaxMind is
// consulted first and may know an address's ASN while knowing nothing about
// where it is, and in that case the top level — and all of v1 — should still
// carry the city DB-IP knows. Values are never blended, only filled in.
//
// Filling is free only between independent facts. The country is settled
// first, by the first source that names one, and the rest of the place is
// taken only from sources that agree with it; otherwise one source's city could
// land beside another's country, describing a place nobody reported. Likewise
// an operator is only ever paired with the network number it was given for,
// and a registered country with its own code.
func (resp Response) primary() geoip.Record {
	var out geoip.Record
	for _, a := range resp.Answers {
		if a.Record.CountryCode != "" {
			out.CountryCode = a.Record.CountryCode
			break
		}
	}

	for _, a := range resp.Answers {
		r := a.Record
		if r.CountryCode == out.CountryCode {
			fillString(&out.City, r.City)
			fillString(&out.Country, r.Country)
			fillString(&out.Continent, r.Continent)
			fillString(&out.ContinentCode, r.ContinentCode)
			fillString(&out.PostalCode, r.PostalCode)
			fillString(&out.TimeZone, r.TimeZone)
			if len(out.Subdivisions) == 0 {
				out.Subdivisions = r.Subdivisions
			}
			if !out.HasCoordinates && r.HasCoordinates {
				out.Latitude, out.Longitude = r.Latitude, r.Longitude
				out.AccuracyRadius = r.AccuracyRadius
				out.HasCoordinates = true
			}
			if out.MetroCode == 0 {
				out.MetroCode = r.MetroCode
			}
			out.InEuropeanUnion = out.InEuropeanUnion || r.InEuropeanUnion
		}

		switch {
		case out.RegisteredCountryCode == "" && r.RegisteredCountryCode != "":
			out.RegisteredCountryCode, out.RegisteredCountry = r.RegisteredCountryCode, r.RegisteredCountry
		case r.RegisteredCountryCode == out.RegisteredCountryCode:
			fillString(&out.RegisteredCountry, r.RegisteredCountry)
		}
		switch {
		case out.ASN == 0 && r.ASN != 0:
			out.ASN, out.ASNOrg = r.ASN, r.ASNOrg
		case r.ASN == out.ASN:
			fillString(&out.ASNOrg, r.ASNOrg)
		}

		// Flags are assertions, so any source asserting one carries.
		out.IsAnycast = out.IsAnycast || r.IsAnycast
		out.IsAnonymousProxy = out.IsAnonymousProxy || r.IsAnonymousProxy
		out.IsSatelliteProvider = out.IsSatelliteProvider || r.IsSatelliteProvider
		out.HasData = out.HasData || r.HasData
	}
	return out
}

func fillString(dst *string, v string) {
	if *dst == "" {
		*dst = v
	}
}

func emptyToNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func family(ip string) string {
	parsed := net.ParseIP(ip)
	switch {
	case parsed == nil:
		return ""
	case parsed.To4() != nil:
		return "ipv4"
	default:
		return "ipv6"
	}
}

// ---------------------------------------------------------------- grouping

// group is one distinct value and every source that reported it.
type group struct {
	Value   string
	Sources []string
}

// groupByValue collapses per-source values into distinct groups, preserving
// the order sources were registered in. Sources with no value are skipped.
func groupByValue(answers []geoip.Answer, value func(geoip.Record) string) []group {
	var groups []group
	for _, a := range answers {
		v := value(a.Record)
		if v == "" {
			continue
		}
		idx := -1
		for i := range groups {
			if groups[i].Value == v {
				idx = i
				break
			}
		}
		if idx < 0 {
			groups = append(groups, group{Value: v, Sources: []string{a.Source}})
			continue
		}
		groups[idx].Sources = append(groups[idx].Sources, a.Source)
	}
	return groups
}

// render writes the groups as one field of the plain-text line. When every
// source agrees there is nothing to attribute, so the value stands alone;
// when they disagree each value carries the sources that reported it.
func (g groups) render() string {
	switch len(g) {
	case 0:
		return ""
	case 1:
		return g[0].Value
	}
	parts := make([]string, 0, len(g))
	for _, grp := range g {
		parts = append(parts, grp.Value+" ["+strings.Join(grp.Sources, ", ")+"]")
	}
	return strings.Join(parts, " / ")
}

type groups []group

// locationSegment is the location half of the plain-text line, in the original
// format: "City [CC] Country", with absent parts omitted. The country is
// written with the name names gives its code, so every source's answer for the
// same country reads identically; a code no source names is shown bare.
func locationSegment(r geoip.Record, names map[string]string) string {
	var parts []string
	if r.City != "" {
		parts = append(parts, r.City)
	}
	switch {
	case r.CountryCode != "" && names[r.CountryCode] != "":
		parts = append(parts, "["+r.CountryCode+"] "+names[r.CountryCode])
	case r.CountryCode != "":
		parts = append(parts, "["+r.CountryCode+"]")
	case r.Country != "":
		parts = append(parts, r.Country)
	}
	return strings.Join(parts, " ")
}

// countryNames settles one display name per country code: the name given by
// the first source, in priority order, that gives one.
//
// Sources name countries differently — IPFire says "United States of America"
// and "Russian Federation" where DB-IP says "United States" and "Russia" — and
// one reports only codes. Grouping on names would report a spelling as a
// disagreement about where the address is. The per-source entries in JSON keep
// each source's own spelling; this is only for comparing and displaying them
// side by side.
func (resp Response) countryNames() map[string]string {
	names := make(map[string]string)
	for _, a := range resp.Answers {
		cc, name := a.Record.CountryCode, a.Record.Country
		if cc == "" || name == "" {
			continue
		}
		if _, seen := names[cc]; !seen {
			names[cc] = name
		}
	}
	return names
}

// asnKey groups ASNs by number, not by label. MaxMind and DB-IP almost never
// spell an organisation identically — "GOOGLE" against "Google LLC" — and
// reporting that as a disagreement about the network would be noise. The label
// shown is the first source's, since they agree on what matters.
func asnKey(r geoip.Record) string {
	if r.ASN == 0 {
		return ""
	}
	return "AS" + strconv.FormatUint(uint64(r.ASN), 10)
}

// asnGroups groups on the number but displays the first label seen for it.
func (resp Response) asnGroups() groups {
	out := groups(groupByValue(resp.Answers, asnKey))
	for i := range out {
		for _, a := range resp.Answers {
			if asnKey(a.Record) == out[i].Value && a.Record.ASNLabel() != "" {
				out[i].Value = a.Record.ASNLabel()
				break
			}
		}
	}
	return out
}

// locationGroups groups on the rendered location, then folds any group whose
// value is a less specific form of another into it.
//
// A source that knows only the country is not contradicting one that also
// knows the city — it is a coarser answer to the same question, and showing
// "Mountain View [US] United States [MaxMind] / [US] United States [DB-IP]"
// reads as a conflict where there is none. Folding only happens when there is
// exactly one more specific candidate, so a genuine split is still shown.
func (resp Response) locationGroups() groups {
	names := resp.countryNames()
	out := groups(groupByValue(resp.Answers, func(r geoip.Record) string {
		return locationSegment(r, names)
	}))
	if len(out) < 2 {
		return out
	}

	kept := out[:0]
	for _, g := range out {
		target := -1
		for j, other := range out {
			if other.Value == g.Value || !strings.HasSuffix(other.Value, g.Value) {
				continue
			}
			if target >= 0 {
				target = -1 // ambiguous: more than one refinement, keep it separate
				break
			}
			target = j
		}
		if target >= 0 {
			out[target].Sources = append(out[target].Sources, g.Sources...)
			continue
		}
		kept = append(kept, g)
	}
	return kept
}

// Agree reports whether every source that had an opinion produced a compatible
// location and the same autonomous system.
func (resp Response) Agree() bool {
	return resp.LocationsAgree() && resp.NetworksAgree()
}

// LocationsAgree is the location half of Agree. A page that shows location and
// network apart needs them apart: sources whose routing data puts an address in
// a different autonomous system than the registry does still agree on where it
// is, and saying otherwise under a heading about location would be false.
func (resp Response) LocationsAgree() bool { return len(resp.locationGroups()) <= 1 }

// NetworksAgree is the network half of Agree.
func (resp Response) NetworksAgree() bool { return len(resp.asnGroups()) <= 1 }

// ---------------------------------------------------------------- plain text

// PlainText keeps the original one-line shape — "IP [City] [[CC] Country]
// [ASN]" — and extends it only where sources disagree, in which case each
// distinct value is attributed and the alternatives are separated by " / ".
//
//	8.8.8.8 Mountain View [US] United States AS15169 (GOOGLE)
//	1.1.1.1 Brisbane [AU] Australia [MaxMind] / [US] United States [DB-IP] AS13335 (APNIC-1)
func (resp Response) PlainText() string {
	parts := []string{resp.IP}
	if loc := resp.locationGroups().render(); loc != "" {
		parts = append(parts, loc)
	}
	if asn := resp.asnGroups().render(); asn != "" {
		parts = append(parts, asn)
	}
	return strings.Join(parts, " ") + "\n"
}

// ---------------------------------------------------------------- json

type subdivision struct {
	Name string  `json:"name"`
	Code *string `json:"code"`
}

type location struct {
	City                  *string       `json:"city"`
	Region                *string       `json:"region"`
	RegionCode            *string       `json:"region_code"`
	Subdivisions          []subdivision `json:"subdivisions"`
	PostalCode            *string       `json:"postal_code"`
	Country               *string       `json:"country"`
	CountryCode           *string       `json:"country_code"`
	Continent             *string       `json:"continent"`
	ContinentCode         *string       `json:"continent_code"`
	InEuropeanUnion       bool          `json:"in_european_union"`
	RegisteredCountry     *string       `json:"registered_country"`
	RegisteredCountryCode *string       `json:"registered_country_code"`
	Latitude              *float64      `json:"latitude"`
	Longitude             *float64      `json:"longitude"`
	AccuracyRadiusKm      *uint16       `json:"accuracy_radius_km"`
	TimeZone              *string       `json:"timezone"`
	LocalTime             *string       `json:"local_time"`
	MetroCode             *uint         `json:"metro_code"`
}

type network struct {
	ASN      *uint   `json:"asn"`
	ASNOrg   *string `json:"asn_org"`
	ASNLabel *string `json:"asn_label"`
}

type recordFlags struct {
	Anycast           bool `json:"anycast"`
	AnonymousProxy    bool `json:"anonymous_proxy"`
	SatelliteProvider bool `json:"satellite_provider"`
}

// sourceEntry is one provider's complete answer.
type sourceEntry struct {
	Source string `json:"source"`
	// Provides names the fields this source can fill for any address, by the
	// keys below. A null or false from a source that provides the field is its
	// answer; from one that does not, it means nothing either way.
	Provides []string    `json:"provides"`
	Location location    `json:"location"`
	Network  network     `json:"network"`
	Flags    recordFlags `json:"flags"`
}

// fieldNames names each geoip field by the response key it governs, in schema
// order. "region" covers the region and its subdivisions, "coordinates" the
// latitude and longitude, and "timezone" the local time derived from it.
var fieldNames = []struct {
	field geoip.Fields
	name  string
}{
	{geoip.FieldCity, "city"},
	{geoip.FieldRegion, "region"},
	{geoip.FieldPostalCode, "postal_code"},
	{geoip.FieldCoordinates, "coordinates"},
	{geoip.FieldAccuracyRadius, "accuracy_radius_km"},
	{geoip.FieldTimeZone, "timezone"},
	{geoip.FieldMetroCode, "metro_code"},
	{geoip.FieldCountry, "country"},
	{geoip.FieldContinent, "continent"},
	{geoip.FieldEuropeanUnion, "in_european_union"},
	{geoip.FieldRegisteredCountry, "registered_country"},
	{geoip.FieldASN, "asn"},
	{geoip.FieldASNOrg, "asn_org"},
	{geoip.FieldAnycast, "anycast"},
	{geoip.FieldAnonymousProxy, "anonymous_proxy"},
	{geoip.FieldSatelliteProvider, "satellite_provider"},
}

func providesList(f geoip.Fields) []string {
	out := make([]string, 0, len(fieldNames))
	for _, fn := range fieldNames {
		if f.Has(fn.field) {
			out = append(out, fn.name)
		}
	}
	return out
}

type tlsOffered struct {
	Version           string   `json:"version"`
	CipherSuites      []string `json:"cipher_suites"`
	Extensions        []string `json:"extensions"`
	SupportedVersions []string `json:"supported_versions"`
	SupportedGroups   []string `json:"supported_groups"`
	// []int, not []uint8: encoding/json treats []uint8 as []byte and
	// base64-encodes it, so this field shipped as "AA==" while the published
	// schema, the client types and the README all documented an array of
	// numbers. Nothing read it, which is why nothing caught it.
	PointFormats   []int    `json:"point_formats"`
	SignatureAlgos []string `json:"signature_algorithms"`
	ALPN           []string `json:"alpn"`
	ServerName     *string  `json:"server_name"`
	GREASE         bool     `json:"grease"`
	// Truncated means the hello was too large to fingerprint. Every field in
	// this block and every hash above it is empty: computing them costs work
	// proportional to whatever the client sent, before the handshake is even
	// complete, and a hello this large is not a real client to identify.
	Truncated bool `json:"truncated"`
}

type tlsNegotiated struct {
	Version     string  `json:"version"`
	CipherSuite string  `json:"cipher_suite"`
	KeyExchange *string `json:"key_exchange"`
	ALPN        *string `json:"alpn"`
	Resumed     bool    `json:"resumed"`
	ECHAccepted bool    `json:"ech_accepted"`
}

type h2Setting struct {
	ID    uint16 `json:"id"`
	Value uint32 `json:"value"`
}

type h2Priority struct {
	StreamID  uint32 `json:"stream_id"`
	Exclusive bool   `json:"exclusive"`
	DependsOn uint32 `json:"depends_on"`
	Weight    uint16 `json:"weight"`
}

type http2Block struct {
	Akamai            string       `json:"akamai"`
	AkamaiHash        string       `json:"akamai_hash"`
	Settings          []h2Setting  `json:"settings"`
	WindowUpdate      uint32       `json:"window_update"`
	Priorities        []h2Priority `json:"priorities"`
	PseudoHeaderOrder []string     `json:"pseudo_header_order"`
}

type tlsBlock struct {
	JA3      string `json:"ja3"`
	JA3Hash  string `json:"ja3_hash"`
	JA3N     string `json:"ja3n"`
	JA3NHash string `json:"ja3n_hash"`

	JA4   string `json:"ja4"`
	JA4R  string `json:"ja4_r"`
	JA4O  string `json:"ja4_o"`
	JA4RO string `json:"ja4_ro"`

	Offered    tlsOffered     `json:"client_hello"`
	Negotiated *tlsNegotiated `json:"negotiated"`
}

type payloadV2 struct {
	Version  int     `json:"version"`
	IP       string  `json:"ip"`
	Family   *string `json:"family"`
	Hostname *string `json:"hostname"`

	// Top-level values come from the first source that had data, so a simple
	// client can ignore the sources array entirely.
	Location location    `json:"location"`
	Network  network     `json:"network"`
	Flags    recordFlags `json:"flags"`

	SourcesAgree bool `json:"sources_agree"`
	// The two halves of sources_agree, for a client showing location and network
	// apart.
	LocationsAgree bool          `json:"locations_agree"`
	NetworksAgree  bool          `json:"networks_agree"`
	Sources        []sourceEntry `json:"sources"`

	// Null unless beacon terminated this connection's TLS itself.
	TLS *tlsBlock `json:"tls"`

	// Null unless the request arrived over HTTP/2.
	HTTP2 *http2Block `json:"http2"`
}

type payloadV1 struct {
	IP          string  `json:"ip"`
	City        *string `json:"city"`
	Country     *string `json:"country"`
	CountryCode *string `json:"country-code"`
	ASN         *string `json:"asn"`
}

func toLocation(r geoip.Record) location {
	regionName, regionCode := r.Region()

	subs := make([]subdivision, 0, len(r.Subdivisions))
	for _, s := range r.Subdivisions {
		subs = append(subs, subdivision{Name: s.Name, Code: emptyToNull(s.Code)})
	}

	loc := location{
		City:                  emptyToNull(r.City),
		Region:                emptyToNull(regionName),
		RegionCode:            emptyToNull(regionCode),
		Subdivisions:          subs,
		PostalCode:            emptyToNull(r.PostalCode),
		Country:               emptyToNull(r.Country),
		CountryCode:           emptyToNull(r.CountryCode),
		Continent:             emptyToNull(r.Continent),
		ContinentCode:         emptyToNull(r.ContinentCode),
		InEuropeanUnion:       r.InEuropeanUnion,
		RegisteredCountry:     emptyToNull(r.RegisteredCountry),
		RegisteredCountryCode: emptyToNull(r.RegisteredCountryCode),
		TimeZone:              emptyToNull(r.TimeZone),
	}

	if r.HasCoordinates {
		lat, lon := r.Latitude, r.Longitude
		loc.Latitude, loc.Longitude = &lat, &lon
		if r.AccuracyRadius != 0 {
			radius := r.AccuracyRadius
			loc.AccuracyRadiusKm = &radius
		}
	}
	if r.MetroCode != 0 {
		metro := r.MetroCode
		loc.MetroCode = &metro
	}
	if r.TimeZone != "" {
		if tz := zone(r.TimeZone); tz != nil {
			loc.LocalTime = emptyToNull(time.Now().In(tz).Format(time.RFC3339))
		}
	}
	return loc
}

// zones caches time.LoadLocation, which reads and parses a file from the
// zoneinfo database on every call — once per source per request otherwise.
var zones sync.Map

func zone(name string) *time.Location {
	if v, ok := zones.Load(name); ok {
		tz, _ := v.(*time.Location)
		return tz
	}
	tz, err := time.LoadLocation(name)
	if err != nil {
		zones.Store(name, (*time.Location)(nil))
		return nil
	}
	zones.Store(name, tz)
	return tz
}

func toNetwork(r geoip.Record) network {
	nw := network{}
	if r.ASN != 0 {
		asn := r.ASN
		nw.ASN = &asn
		nw.ASNOrg = emptyToNull(r.ASNOrg)
		nw.ASNLabel = emptyToNull(r.ASNLabel())
	}
	return nw
}

func toFlags(r geoip.Record) recordFlags {
	return recordFlags{
		Anycast:           r.IsAnycast,
		AnonymousProxy:    r.IsAnonymousProxy,
		SatelliteProvider: r.IsSatelliteProvider,
	}
}

func (resp Response) v2() payloadV2 {
	primary := resp.primary()

	sources := make([]sourceEntry, 0, len(resp.Answers))
	for _, a := range resp.Answers {
		sources = append(sources, sourceEntry{
			Source:   a.Source,
			Provides: providesList(a.Provides),
			Location: toLocation(a.Record),
			Network:  toNetwork(a.Record),
			Flags:    toFlags(a.Record),
		})
	}

	return payloadV2{
		TLS:            resp.tlsBlock(),
		HTTP2:          resp.http2Block(),
		Version:        SchemaVersion,
		IP:             resp.IP,
		Family:         emptyToNull(family(resp.IP)),
		Hostname:       emptyToNull(resp.Hostname),
		Location:       toLocation(primary),
		Network:        toNetwork(primary),
		Flags:          toFlags(primary),
		SourcesAgree:   resp.Agree(),
		LocationsAgree: resp.LocationsAgree(),
		NetworksAgree:  resp.NetworksAgree(),
		Sources:        sources,
	}
}

func hexList(vals []uint16) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, fmt.Sprintf("0x%04x", v))
	}
	return out
}

func (resp Response) tlsBlock() *tlsBlock {
	fp := resp.TLS
	if fp == nil {
		return nil
	}
	b := &tlsBlock{
		JA3: fp.JA3, JA3Hash: fp.JA3Hash, JA3N: fp.JA3N, JA3NHash: fp.JA3NHash,
		JA4: fp.JA4, JA4R: fp.JA4R, JA4O: fp.JA4O, JA4RO: fp.JA4RO,
		Offered: tlsOffered{
			Version:           fp.TLSVersion,
			CipherSuites:      hexList(fp.CipherSuites),
			Extensions:        hexList(fp.Extensions),
			SupportedVersions: hexList(fp.SupportedTLS),
			SupportedGroups:   hexList(fp.Curves),
			PointFormats:      intList(fp.PointFormats),
			SignatureAlgos:    hexList(fp.SignatureAlgos),
			// Re-made rather than passed through: a nil slice encodes as null,
			// and every list in this schema is documented as an array. The
			// client dereferences it without a guard, so null took the page
			// down rather than degrading a row.
			ALPN:       strList(fp.ALPN),
			ServerName: emptyToNull(fp.ServerName),
			GREASE:     fp.GREASE,
			Truncated:  fp.Truncated,
		},
	}
	if n := resp.Negotiated; n != nil {
		b.Negotiated = &tlsNegotiated{
			Version:     n.Version,
			CipherSuite: n.CipherSuite,
			KeyExchange: emptyToNull(n.CurveID),
			ALPN:        emptyToNull(n.ALPN),
			Resumed:     n.Resumed,
			ECHAccepted: n.ECHAccepted,
		}
	}
	return b
}

func (resp Response) http2Block() *http2Block {
	fp := resp.HTTP2
	if fp == nil {
		return nil
	}
	settings := make([]h2Setting, 0, len(fp.Settings))
	for _, s := range fp.Settings {
		settings = append(settings, h2Setting{ID: s.ID, Value: s.Value})
	}
	priorities := make([]h2Priority, 0, len(fp.Priorities))
	for _, pr := range fp.Priorities {
		priorities = append(priorities, h2Priority{
			StreamID:  pr.StreamID,
			Exclusive: pr.Exclusive,
			DependsOn: pr.DependsOn,
			Weight:    pr.Weight,
		})
	}
	return &http2Block{
		Akamai:            fp.Raw,
		AkamaiHash:        fp.Hash,
		Settings:          settings,
		WindowUpdate:      fp.WindowUpdate,
		Priorities:        priorities,
		PseudoHeaderOrder: strList(fp.PseudoHeaderOrder),
	}
}

// intList and strList turn a possibly-nil slice into one that encodes as [].
// Go's nil slice becomes JSON null, which is a different statement from "this
// client offered none" and is not what the schema promises.
func intList(in []uint8) []int {
	out := make([]int, 0, len(in))
	for _, v := range in {
		out = append(out, int(v))
	}
	return out
}

func strList(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func (resp Response) v1() payloadV1 {
	r := resp.primary()
	return payloadV1{
		IP:          resp.IP,
		City:        emptyToNull(r.City),
		Country:     emptyToNull(r.Country),
		CountryCode: emptyToNull(r.CountryCode),
		ASN:         emptyToNull(r.ASNLabel()),
	}
}

func (resp Response) JSON(version int) ([]byte, error) {
	// v1 stays compact: it is the frozen legacy shape, and callers that pipe it
	// through grep or cut would break on a pretty-printed body.
	var payload any
	indent := false
	if version == 1 {
		payload = resp.v1()
	} else {
		payload, indent = resp.v2(), true
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func (resp Response) Write(w http.ResponseWriter, asJSON bool, version int) {
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")

	if asJSON {
		body, err := resp.JSON(version)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"internal error"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(resp.PlainText()))
}
