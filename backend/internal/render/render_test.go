package render

import (
	"encoding/json"
	"strings"
	"testing"

	"beacon/internal/geoip"
	"beacon/internal/h2fp"
	"beacon/internal/tlsfp"
)

func full() geoip.Record {
	return geoip.Record{
		City:          "Mountain View",
		Country:       "United States",
		CountryCode:   "US",
		Continent:     "North America",
		ContinentCode: "NA",
		Subdivisions: []geoip.Subdivision{
			{Name: "California", Code: "CA"},
		},
		PostalCode:            "94035",
		Latitude:              37.386,
		Longitude:             -122.0838,
		AccuracyRadius:        1000,
		TimeZone:              "America/Los_Angeles",
		MetroCode:             807,
		HasCoordinates:        true,
		RegisteredCountry:     "United States",
		RegisteredCountryCode: "US",
		IsAnycast:             true,
		ASN:                   15169,
		ASNOrg:                "GOOGLE",
		HasData:               true,
	}
}

func one(src string, rec geoip.Record) []geoip.Answer {
	return []geoip.Answer{{Source: src, Record: rec}}
}

func decode(t *testing.T, resp Response, version int) map[string]any {
	t.Helper()
	body, err := resp.JSON(version)
	if err != nil {
		t.Fatalf("JSON(%d): %v", version, err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

// ------------------------------------------------------------- plain text

func TestPlainTextSingleSource(t *testing.T) {
	got := New("8.8.8.8", "dns.google", one("MaxMind", full())).PlainText()
	want := "8.8.8.8 Mountain View [US] United States AS15169 (GOOGLE)\n"
	if got != want {
		t.Errorf("PlainText()\n got %q\nwant %q", got, want)
	}
}

// Agreeing sources are not attributed — there is nothing to disambiguate.
func TestPlainTextAgreeingSourcesAreUnlabelled(t *testing.T) {
	answers := []geoip.Answer{
		{Source: "MaxMind", Record: full()},
		{Source: "DB-IP", Record: full()},
	}
	got := New("8.8.8.8", "", answers).PlainText()
	want := "8.8.8.8 Mountain View [US] United States AS15169 (GOOGLE)\n"
	if got != want {
		t.Errorf("PlainText()\n got %q\nwant %q", got, want)
	}
	if !New("8.8.8.8", "", answers).Agree() {
		t.Error("Agree() = false, want true")
	}
}

// Disagreeing sources are each attributed, alternatives separated by " / ".
func TestPlainTextDisagreeingSourcesAreLabelled(t *testing.T) {
	phoenix := full()
	phoenix.City = "Phoenix"
	phoenix.Subdivisions = []geoip.Subdivision{{Name: "Arizona", Code: "AZ"}}

	answers := []geoip.Answer{
		{Source: "MaxMind", Record: full()},
		{Source: "RIPE", Record: full()},
		{Source: "2IP", Record: phoenix},
	}

	got := New("1.1.1.1", "", answers).PlainText()
	want := "1.1.1.1 Mountain View [US] United States [MaxMind, RIPE] / Phoenix [US] United States [2IP] AS15169 (GOOGLE)\n"
	if got != want {
		t.Errorf("PlainText()\n got %q\nwant %q", got, want)
	}
	if New("1.1.1.1", "", answers).Agree() {
		t.Error("Agree() = true, want false")
	}
}

// Sources spelling the same AS organisation differently are not disagreeing
// about the network, so the label is shown once, unattributed.
func TestPlainTextASNOrgSpellingIsNotDisagreement(t *testing.T) {
	other := full()
	other.ASNOrg = "Google LLC"

	answers := []geoip.Answer{
		{Source: "MaxMind", Record: full()},
		{Source: "DB-IP", Record: other},
	}
	resp := New("8.8.8.8", "", answers)
	want := "8.8.8.8 Mountain View [US] United States AS15169 (GOOGLE)\n"
	if got := resp.PlainText(); got != want {
		t.Errorf("PlainText()\n got %q\nwant %q", got, want)
	}
	if !resp.Agree() {
		t.Error("Agree() = false for a cosmetic org-name difference, want true")
	}
}

// A genuinely different AS number is a real disagreement and is attributed.
func TestPlainTextASNNumberDisagreement(t *testing.T) {
	other := full()
	other.ASN, other.ASNOrg = 213520, "Senko Digital LLC"

	answers := []geoip.Answer{
		{Source: "MaxMind", Record: full()},
		{Source: "DB-IP", Record: other},
	}
	resp := New("8.8.8.8", "", answers)
	want := "8.8.8.8 Mountain View [US] United States AS15169 (GOOGLE) [MaxMind] / AS213520 (Senko Digital LLC) [DB-IP]\n"
	if got := resp.PlainText(); got != want {
		t.Errorf("PlainText()\n got %q\nwant %q", got, want)
	}
	if resp.Agree() {
		t.Error("Agree() = true for different AS numbers, want false")
	}
}

// A source that knows only the country is giving a coarser answer, not a
// conflicting one, so it folds into the more specific group.
func TestPlainTextCoarserLocationFolds(t *testing.T) {
	coarse := full()
	coarse.City = ""
	coarse.Subdivisions = nil

	answers := []geoip.Answer{
		{Source: "MaxMind", Record: full()},
		{Source: "DB-IP", Record: coarse},
	}
	resp := New("8.8.8.8", "", answers)
	want := "8.8.8.8 Mountain View [US] United States AS15169 (GOOGLE)\n"
	if got := resp.PlainText(); got != want {
		t.Errorf("PlainText()\n got %q\nwant %q", got, want)
	}
	if !resp.Agree() {
		t.Error("Agree() = false for a coarser-but-compatible location, want true")
	}
}

// Merging is per field: MaxMind knowing only the ASN must not null out the
// city DB-IP knows, at the top level or in v1.
func TestPrimaryMergesPerField(t *testing.T) {
	asnOnly := geoip.Record{ASN: 15169, ASNOrg: "GOOGLE", HasData: true}
	cityOnly := geoip.Record{City: "Mountain View", Country: "United States", CountryCode: "US", HasData: true}

	answers := []geoip.Answer{
		{Source: "MaxMind", Record: asnOnly},
		{Source: "DB-IP", Record: cityOnly},
	}
	m := decode(t, New("8.8.8.8", "", answers), 1)
	if m["city"] != "Mountain View" || m["asn"] != "AS15169 (GOOGLE)" {
		t.Errorf("v1 = %v, want both the city and the ASN filled", m)
	}
}

// A source with nothing to say is skipped rather than producing an empty group.
func TestPlainTextSkipsEmptySources(t *testing.T) {
	answers := []geoip.Answer{
		{Source: "MaxMind", Record: full()},
		{Source: "DB-IP", Record: geoip.Record{HasData: true}},
	}
	got := New("8.8.8.8", "", answers).PlainText()
	want := "8.8.8.8 Mountain View [US] United States AS15169 (GOOGLE)\n"
	if got != want {
		t.Errorf("PlainText()\n got %q\nwant %q", got, want)
	}
}

func TestPlainTextNoSources(t *testing.T) {
	if got := New("10.0.0.1", "", nil).PlainText(); got != "10.0.0.1\n" {
		t.Errorf("PlainText() = %q, want %q", got, "10.0.0.1\n")
	}
}

// ------------------------------------------------------------------- json

// v1 is frozen: existing clients read these exact five keys.
func TestV1ShapeUnchanged(t *testing.T) {
	m := decode(t, New("8.8.8.8", "dns.google", one("MaxMind", full())), 1)

	want := map[string]any{
		"ip":           "8.8.8.8",
		"city":         "Mountain View",
		"country":      "United States",
		"country-code": "US",
		"asn":          "AS15169 (GOOGLE)",
	}
	if len(m) != len(want) {
		t.Errorf("v1 has %d keys, want exactly %d", len(m), len(want))
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("v1[%q] = %v, want %v", k, m[k], v)
		}
	}
}

func TestV2TopLevelAndSources(t *testing.T) {
	phoenix := full()
	phoenix.City = "Phoenix"

	answers := []geoip.Answer{
		{Source: "MaxMind", Record: full()},
		{Source: "DB-IP", Record: phoenix},
	}
	m := decode(t, New("8.8.8.8", "dns.google", answers), SchemaVersion)

	if m["version"] != float64(SchemaVersion) || m["family"] != "ipv4" || m["hostname"] != "dns.google" {
		t.Errorf("header fields wrong: %v %v %v", m["version"], m["family"], m["hostname"])
	}
	if m["sources_agree"] != false {
		t.Errorf("sources_agree = %v, want false", m["sources_agree"])
	}

	// Top level mirrors the first source so simple clients need read no further.
	loc := m["location"].(map[string]any)
	if loc["city"] != "Mountain View" {
		t.Errorf("top-level city = %v, want Mountain View", loc["city"])
	}

	srcs := m["sources"].([]any)
	if len(srcs) != 2 {
		t.Fatalf("sources has %d entries, want 2", len(srcs))
	}
	first := srcs[0].(map[string]any)
	second := srcs[1].(map[string]any)
	if first["source"] != "MaxMind" || second["source"] != "DB-IP" {
		t.Errorf("source order = %v, %v", first["source"], second["source"])
	}
	if second["location"].(map[string]any)["city"] != "Phoenix" {
		t.Errorf("DB-IP city = %v, want Phoenix", second["location"].(map[string]any)["city"])
	}
	// Each entry carries the full record, not a diff.
	for _, key := range []string{"location", "network", "flags"} {
		if _, ok := first[key]; !ok {
			t.Errorf("source entry missing %q", key)
		}
	}
}

func TestV2AllFieldsPresent(t *testing.T) {
	m := decode(t, New("8.8.8.8", "dns.google", one("MaxMind", full())), SchemaVersion)

	loc := m["location"].(map[string]any)
	for k, want := range map[string]any{
		"region":             "California",
		"region_code":        "CA",
		"postal_code":        "94035",
		"continent":          "North America",
		"continent_code":     "NA",
		"latitude":           37.386,
		"timezone":           "America/Los_Angeles",
		"country_code":       "US",
		"metro_code":         float64(807),
		"accuracy_radius_km": float64(1000),
	} {
		if loc[k] != want {
			t.Errorf("location[%q] = %v, want %v", k, loc[k], want)
		}
	}

	lt, ok := loc["local_time"].(string)
	if !ok || lt == "" {
		t.Fatalf("local_time = %v, want an RFC3339 timestamp", loc["local_time"])
	}
	if strings.HasSuffix(lt, "Z") {
		t.Errorf("local_time = %q, want a zone offset for America/Los_Angeles", lt)
	}

	if nw := m["network"].(map[string]any); nw["asn"] != float64(15169) || nw["asn_org"] != "GOOGLE" {
		t.Errorf("network = %v", nw)
	}
	if fl := m["flags"].(map[string]any); fl["anycast"] != true {
		t.Errorf("flags.anycast = %v, want true", fl["anycast"])
	}
}

func TestV2EmptyIsAllNull(t *testing.T) {
	m := decode(t, New("203.0.113.1", "", nil), SchemaVersion)

	if m["hostname"] != nil {
		t.Errorf("hostname = %v, want null", m["hostname"])
	}
	if srcs := m["sources"].([]any); len(srcs) != 0 {
		t.Errorf("sources = %v, want empty", srcs)
	}
	if m["sources_agree"] != true {
		t.Errorf("sources_agree = %v, want true when there is nothing to disagree about", m["sources_agree"])
	}
	loc := m["location"].(map[string]any)
	for _, k := range []string{"city", "country", "latitude", "timezone", "local_time"} {
		if loc[k] != nil {
			t.Errorf("location[%q] = %v, want null", k, loc[k])
		}
	}
}

// 0,0 is a real coordinate; HasCoordinates distinguishes it from absent.
func TestV2NullIslandIsNotAbsent(t *testing.T) {
	m := decode(t, New("1.2.3.4", "", one("X", geoip.Record{HasCoordinates: true, HasData: true})), SchemaVersion)
	loc := m["location"].(map[string]any)
	if loc["latitude"] != float64(0) || loc["longitude"] != float64(0) {
		t.Errorf("lat/lon = %v/%v, want 0/0 present", loc["latitude"], loc["longitude"])
	}
}

func TestFamilyIPv6(t *testing.T) {
	m := decode(t, New("2606:4700::1111", "", nil), SchemaVersion)
	if m["family"] != "ipv6" {
		t.Errorf("family = %v, want ipv6", m["family"])
	}
}

func TestASNLabel(t *testing.T) {
	if got := (geoip.Record{ASN: 64512}).ASNLabel(); got != "AS64512" {
		t.Errorf("ASNLabel() = %q, want AS64512", got)
	}
	if got := (geoip.Record{}).ASNLabel(); got != "" {
		t.Errorf("ASNLabel() = %q, want empty", got)
	}
}

// v1 is the frozen legacy body: compact, so scripts that grep or cut a
// single-line response keep working. v2 is indented for humans.
func TestV1IsCompactV2IsIndented(t *testing.T) {
	resp := New("8.8.8.8", "dns.google", one("MaxMind", full()))

	v1, err := resp.JSON(1)
	if err != nil {
		t.Fatalf("JSON(1): %v", err)
	}
	if strings.Contains(string(v1), "\n") {
		t.Errorf("v1 body contains a newline, want one line:\n%s", v1)
	}

	v2, err := resp.JSON(SchemaVersion)
	if err != nil {
		t.Fatalf("JSON(2): %v", err)
	}
	if !strings.Contains(string(v2), "\n") {
		t.Error("v2 body is not indented")
	}
}

// The TLS block is absent for a request beacon did not terminate itself —
// behind a TLS-terminating proxy the ClientHello is gone.
func TestTLSBlockAbsentWithoutTLS(t *testing.T) {
	m := decode(t, New("8.8.8.8", "", one("MaxMind", full())), SchemaVersion)
	if m["tls"] != nil {
		t.Errorf("tls = %v, want null for a plain HTTP request", m["tls"])
	}
}

func TestTLSBlockPresentWithFingerprint(t *testing.T) {
	fp := &tlsfp.Fingerprint{
		JA3: "771,4867,43,29,0", JA3Hash: "375c6162a492dfbf2795909110ce8424",
		JA3N: "771,4867,43,29,0", JA3NHash: "90a369ebd76665d3296677774ee22ea2",
		JA4:  "t13d4907h2_0d8feac7bc37_7395dae3b2f3",
		JA4R: "t13d4907h2_0004_000a", JA4O: "t13d4907h2_x_y", JA4RO: "t13d4907h2_a_b",
		TLSVersion:   "TLS 1.3",
		CipherSuites: []uint16{0x1303, 0x1302},
		Extensions:   []uint16{0x002b, 0x0000},
		Curves:       []uint16{29},
		ALPN:         []string{"h2"},
		ServerName:   "beacon.example.com",
		GREASE:       true,
	}
	resp := New("8.8.8.8", "", one("MaxMind", full())).
		WithTLS(fp, &NegotiatedTLS{
			Version: "TLS 1.3", CipherSuite: "TLS_AES_128_GCM_SHA256",
			CurveID: "x25519", ALPN: "h2",
		})

	m := decode(t, resp, SchemaVersion)
	tlsBlk, ok := m["tls"].(map[string]any)
	if !ok {
		t.Fatalf("tls = %v, want an object", m["tls"])
	}
	for k, want := range map[string]any{
		"ja3_hash": "375c6162a492dfbf2795909110ce8424",
		"ja4":      "t13d4907h2_0d8feac7bc37_7395dae3b2f3",
	} {
		if tlsBlk[k] != want {
			t.Errorf("tls[%q] = %v, want %v", k, tlsBlk[k], want)
		}
	}

	hello := tlsBlk["client_hello"].(map[string]any)
	if hello["grease"] != true || hello["server_name"] != "beacon.example.com" {
		t.Errorf("client_hello = %v", hello)
	}
	if got := hello["cipher_suites"].([]any); len(got) != 2 || got[0] != "0x1303" {
		t.Errorf("cipher_suites = %v, want hex strings", got)
	}
	neg := tlsBlk["negotiated"].(map[string]any)
	if neg["key_exchange"] != "x25519" || neg["alpn"] != "h2" {
		t.Errorf("negotiated = %v", neg)
	}
}

// v1 is frozen and must not grow a TLS block.
func TestV1HasNoTLSBlock(t *testing.T) {
	resp := New("8.8.8.8", "", one("MaxMind", full())).
		WithTLS(&tlsfp.Fingerprint{JA3Hash: "x"}, nil)
	m := decode(t, resp, 1)
	if _, ok := m["tls"]; ok {
		t.Error("v1 grew a tls key")
	}
	if len(m) != 5 {
		t.Errorf("v1 has %d keys, want 5", len(m))
	}
}

func TestHTTP2BlockAbsentForHTTP11(t *testing.T) {
	m := decode(t, New("8.8.8.8", "", one("MaxMind", full())), SchemaVersion)
	if m["http2"] != nil {
		t.Errorf("http2 = %v, want null for a non-HTTP/2 request", m["http2"])
	}
}

func TestHTTP2BlockPresent(t *testing.T) {
	fp := &h2fp.Fingerprint{
		Raw:  "3:100;4:10485760;2:0|1048510465|0|m,s,a,p",
		Hash: "64a832f547be33249bf4d33e8a46c5dc",
		Settings: []h2fp.Setting{
			{ID: 3, Value: 100}, {ID: 4, Value: 10485760}, {ID: 2, Value: 0},
		},
		WindowUpdate:      1048510465,
		PseudoHeaderOrder: []string{"m", "s", "a", "p"},
	}
	m := decode(t, New("8.8.8.8", "", one("MaxMind", full())).WithHTTP2(fp), SchemaVersion)

	blk, ok := m["http2"].(map[string]any)
	if !ok {
		t.Fatalf("http2 = %v, want an object", m["http2"])
	}
	if blk["akamai"] != fp.Raw || blk["akamai_hash"] != fp.Hash {
		t.Errorf("http2 = %v", blk)
	}
	if got := blk["settings"].([]any); len(got) != 3 {
		t.Errorf("settings has %d entries, want 3", len(got))
	}
	// A zero-valued setting must survive the round trip.
	last := blk["settings"].([]any)[2].(map[string]any)
	if last["id"] != float64(2) || last["value"] != float64(0) {
		t.Errorf("last setting = %v, want id 2 value 0", last)
	}
	if got := blk["pseudo_header_order"].([]any); len(got) != 4 || got[0] != "m" {
		t.Errorf("pseudo_header_order = %v", got)
	}
}

func TestV1HasNoHTTP2Block(t *testing.T) {
	resp := New("8.8.8.8", "", one("MaxMind", full())).
		WithHTTP2(&h2fp.Fingerprint{Hash: "x"})
	m := decode(t, resp, 1)
	if _, ok := m["http2"]; ok {
		t.Error("v1 grew an http2 key")
	}
	if len(m) != 5 {
		t.Errorf("v1 has %d keys, want 5", len(m))
	}
}
