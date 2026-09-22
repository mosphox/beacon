package tlsfp

import (
	"crypto/tls"
	"strings"
	"testing"
)

// curlHello reproduces the ClientHello that curl 8.7.1 sent to
// tls.browserleaks.com, field for field, as the standard library parses it.
//
// The expected outputs below are what BrowserLeaks computed for that exact
// handshake, so this pins our implementation against an independent one rather
// than against itself.
func curlHello() *tls.ClientHelloInfo {
	ciphers := []uint16{
		4867, 4866, 4865, 52393, 52392, 52394, 49200, 49196, 49192, 49188,
		49172, 49162, 159, 107, 57, 65413, 196, 136, 129, 157, 61, 53, 192,
		132, 49199, 49195, 49191, 49187, 49171, 49161, 158, 103, 51, 190, 69,
		156, 60, 47, 186, 65, 49169, 49159, 5, 4, 49170, 49160, 22, 10, 255,
	}
	curves := []tls.CurveID{29, 23, 24, 25}
	sigs := []tls.SignatureScheme{
		0x0806, 0x0601, 0x0603, 0x0805, 0x0501, 0x0503,
		0x0804, 0x0401, 0x0403, 0x0201, 0x0203,
	}
	return &tls.ClientHelloInfo{
		CipherSuites:      ciphers,
		ServerName:        "tls.browserleaks.com",
		SupportedCurves:   curves,
		SupportedPoints:   []uint8{0},
		SignatureSchemes:  sigs,
		SupportedProtos:   []string{"h2", "http/1.1"},
		SupportedVersions: []uint16{772, 771, 770, 769},
		// Wire order: supported_versions, key_share, server_name,
		// ec_point_formats, supported_groups, signature_algorithms, ALPN.
		Extensions: []uint16{43, 51, 0, 11, 10, 13, 16},
	}
}

func TestMatchesBrowserLeaksForCurl(t *testing.T) {
	fp := New(curlHello())

	cases := []struct{ name, got, want string }{
		{"ja3_hash", fp.JA3Hash, "375c6162a492dfbf2795909110ce8424"},
		{"ja3n_hash", fp.JA3NHash, "90a369ebd76665d3296677774ee22ea2"},
		{"ja4", fp.JA4, "t13d4907h2_0d8feac7bc37_7395dae3b2f3"},
		{"ja4_o", fp.JA4O, "t13d4907h2_2677ac475d6b_c6f9150fbe3b"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.name, c.got, c.want)
		}
	}
}

func TestJA3TextMatchesBrowserLeaks(t *testing.T) {
	fp := New(curlHello())

	wantJA3 := "771,4867-4866-4865-52393-52392-52394-49200-49196-49192-49188-" +
		"49172-49162-159-107-57-65413-196-136-129-157-61-53-192-132-49199-" +
		"49195-49191-49187-49171-49161-158-103-51-190-69-156-60-47-186-65-" +
		"49169-49159-5-4-49170-49160-22-10-255,43-51-0-11-10-13-16,29-23-24-25,0"
	if fp.JA3 != wantJA3 {
		t.Errorf("ja3_text\n got %s\nwant %s", fp.JA3, wantJA3)
	}

	// JA3N differs only in that the extensions are sorted.
	wantExts := "0-10-11-13-16-43-51"
	if !strings.Contains(fp.JA3N, ","+wantExts+",") {
		t.Errorf("ja3n_text extensions not sorted: %s", fp.JA3N)
	}
}

// The raw variants show which extensions each form includes: the sorted form
// drops server_name and ALPN, the wire-order form keeps them, and the prefix
// counts all of them either way.
func TestJA4RawVariantsMatchBrowserLeaks(t *testing.T) {
	fp := New(curlHello())

	wantR := "t13d4907h2_" +
		"0004,0005,000a,0016,002f,0033,0035,0039,003c,003d,0041,0045,0067," +
		"006b,0081,0084,0088,009c,009d,009e,009f,00ba,00be,00c0,00c4,00ff," +
		"1301,1302,1303,c007,c008,c009,c00a,c011,c012,c013,c014,c023,c024," +
		"c027,c028,c02b,c02c,c02f,c030,cca8,cca9,ccaa,ff85_" +
		"000a,000b,000d,002b,0033_" +
		"0806,0601,0603,0805,0501,0503,0804,0401,0403,0201,0203"
	if fp.JA4R != wantR {
		t.Errorf("ja4_r\n got %s\nwant %s", fp.JA4R, wantR)
	}

	wantRO := "t13d4907h2_" +
		"1303,1302,1301,cca9,cca8,ccaa,c030,c02c,c028,c024,c014,c00a,009f," +
		"006b,0039,ff85,00c4,0088,0081,009d,003d,0035,00c0,0084,c02f,c02b," +
		"c027,c023,c013,c009,009e,0067,0033,00be,0045,009c,003c,002f,00ba," +
		"0041,c011,c007,0005,0004,c012,c008,0016,000a,00ff_" +
		"002b,0033,0000,000b,000a,000d,0010_" +
		"0806,0601,0603,0805,0501,0503,0804,0401,0403,0201,0203"
	if fp.JA4RO != wantRO {
		t.Errorf("ja4_ro\n got %s\nwant %s", fp.JA4RO, wantRO)
	}
}

// GREASE values are random per connection. If they reached the fingerprint,
// every Chrome handshake would look like a different client.
func TestGREASEIsExcluded(t *testing.T) {
	plain := New(curlHello())

	greased := curlHello()
	greased.CipherSuites = append([]uint16{0x0a0a}, greased.CipherSuites...)
	greased.Extensions = append([]uint16{0x1a1a}, greased.Extensions...)
	greased.Extensions = append(greased.Extensions, 0xfafa)
	greased.SupportedCurves = append([]tls.CurveID{0x2a2a}, greased.SupportedCurves...)
	greased.SupportedVersions = append([]uint16{0x3a3a}, greased.SupportedVersions...)

	g := New(greased)
	for _, c := range []struct{ name, a, b string }{
		{"ja3", plain.JA3, g.JA3},
		{"ja3_hash", plain.JA3Hash, g.JA3Hash},
		{"ja4", plain.JA4, g.JA4},
		{"ja4_r", plain.JA4R, g.JA4R},
		{"ja4_ro", plain.JA4RO, g.JA4RO},
	} {
		if c.a != c.b {
			t.Errorf("%s changed when GREASE was added:\n without %s\n with    %s", c.name, c.a, c.b)
		}
	}
	if !g.GREASE {
		t.Error("GREASE flag not set although GREASE values were present")
	}
	if plain.GREASE {
		t.Error("GREASE flag set for a hello with none")
	}
}

func TestIsGREASE(t *testing.T) {
	for _, v := range []uint16{0x0a0a, 0x1a1a, 0x2a2a, 0x3a3a, 0x8a8a, 0xfafa} {
		if !isGREASE(v) {
			t.Errorf("isGREASE(0x%04x) = false, want true", v)
		}
	}
	for _, v := range []uint16{0x1301, 0x0000, 0x0a0b, 0x1a0a, 0xc02f, 0x002b} {
		if isGREASE(v) {
			t.Errorf("isGREASE(0x%04x) = true, want false", v)
		}
	}
}

// Extension permutation is why JA3N and the sorted JA4 exist: Chrome shuffles
// its extension order per connection, so the wire-order forms move and the
// sorted forms must not.
func TestExtensionPermutationOnlyMovesWireOrderForms(t *testing.T) {
	a := New(curlHello())

	shuffled := curlHello()
	shuffled.Extensions = []uint16{13, 16, 43, 10, 51, 0, 11}

	b := New(shuffled)
	if a.JA3N != b.JA3N {
		t.Error("ja3n changed under extension permutation")
	}
	if a.JA4 != b.JA4 {
		t.Error("ja4 changed under extension permutation")
	}
	if a.JA3 == b.JA3 {
		t.Error("ja3 did not change under extension permutation; it should")
	}
	if a.JA4O == b.JA4O {
		t.Error("ja4_o did not change under extension permutation; it should")
	}
}

func TestALPNCode(t *testing.T) {
	cases := map[string]string{
		"h2": "h2", "http/1.1": "h1", "h3": "h3", "": "00", "spdy/3.1": "s1",
		// A single character is used as both ends.
		"x": "xx",
		// Non-alphanumeric ends fall back to hex: the first hex digit of the
		// first byte and the last of the last byte. These are the
		// specification's own worked examples.
		"\xab":       "ab",
		" ":          "20",
		"\xab\xcd":   "ad",
		" a":         "21",
		"0\xab":      "3b",
		"a ":         "60",
		"01\xab\xcd": "3d",
		// Printable but not alphanumeric still takes the hex path.
		"spdy/": "7f",
		"_grpc": "53",
	}
	for in, want := range cases {
		var protos []string
		if in != "" {
			protos = []string{in}
		}
		if got := alpnCode(protos); got != want {
			t.Errorf("alpnCode(%q) = %q, want %q", in, got, want)
		}
	}
	if got := alpnCode(nil); got != "00" {
		t.Errorf("alpnCode(nil) = %q, want 00", got)
	}
}

// Without the SNI extension the prefix flips from "d" (domain) to "i" (IP
// literal), and the extension count drops with it.
func TestNoSNIFlipsIndicator(t *testing.T) {
	h := curlHello()
	h.ServerName = ""
	h.Extensions = []uint16{43, 51, 11, 10, 13, 16} // server_name removed

	got := New(h).JA4
	if !strings.HasPrefix(got, "t13i") {
		t.Errorf("ja4 = %q, want the t13i prefix when SNI is absent", got)
	}
	if !strings.HasPrefix(got, "t13i4906") {
		t.Errorf("ja4 = %q, want 06 extensions once server_name is gone", got)
	}
}

// The flag keys off the SNI extension being present, not off a hostname being
// parsed out of it. Go only fills ServerName for a host_name entry, so an SNI
// extension carrying some other name type leaves it empty — the spec still
// calls that "d".
func TestSNIIndicatorFollowsTheExtensionNotTheHostname(t *testing.T) {
	h := curlHello()
	h.ServerName = "" // extension still listed
	if got := New(h).JA4; !strings.HasPrefix(got, "t13d") {
		t.Errorf("ja4 = %q, want t13d: the SNI extension is present", got)
	}
}

// JA3's first field is the legacy version, which is 0x0303 whenever
// supported_versions is present — even for a TLS 1.3 client.
func TestLegacyVersionField(t *testing.T) {
	if got := New(curlHello()).JA3; !strings.HasPrefix(got, "771,") {
		t.Errorf("ja3 starts %.8q, want 771 when supported_versions is present", got)
	}

	old := curlHello()
	old.Extensions = []uint16{0, 11, 10, 13, 16} // no supported_versions
	old.SupportedVersions = []uint16{771, 770, 769}
	if got := New(old).JA3; !strings.HasPrefix(got, "771,") {
		t.Errorf("ja3 starts %.8q, want the legacy max", got)
	}
}

func TestEmptyHelloDoesNotPanic(t *testing.T) {
	fp := New(&tls.ClientHelloInfo{})
	if fp.JA3Hash == "" || fp.JA4 == "" {
		t.Error("empty hello produced no fingerprint")
	}
	if !strings.Contains(fp.JA4, "000000000000") {
		t.Errorf("ja4 = %q, want zeroed digests for an empty hello", fp.JA4)
	}
}

// An oversized hello is not fingerprinted at all: no hashes, no echoes, no
// work done proportional to what the client sent.
func TestOversizedHelloIsNotFingerprinted(t *testing.T) {
	for name, mutate := range map[string]func(*tls.ClientHelloInfo){
		"ciphers": func(h *tls.ClientHelloInfo) {
			h.CipherSuites = make([]uint16, maxListEntries+1)
		},
		"extensions": func(h *tls.ClientHelloInfo) {
			h.Extensions = make([]uint16, maxListEntries+1)
		},
		"curves": func(h *tls.ClientHelloInfo) {
			h.SupportedCurves = make([]tls.CurveID, maxListEntries+1)
		},
		"sigalgs": func(h *tls.ClientHelloInfo) {
			h.SignatureSchemes = make([]tls.SignatureScheme, maxListEntries+1)
		},
		// Both of these are echoed back verbatim on every response rather than
		// only hashed, which made one handshake a lasting egress multiplier.
		"alpn": func(h *tls.ClientHelloInfo) {
			h.SupportedProtos = make([]string, maxALPNEntries+1)
		},
		"servername": func(h *tls.ClientHelloInfo) {
			h.ServerName = strings.Repeat("a", maxServerName+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := curlHello()
			mutate(h)

			fp := New(h)
			if !fp.Truncated {
				t.Fatal("Truncated = false for an oversized hello")
			}
			for field, got := range map[string]string{
				"ja3": fp.JA3, "ja3n": fp.JA3N, "ja3_hash": fp.JA3Hash,
				"ja3n_hash": fp.JA3NHash, "ja4": fp.JA4, "ja4_r": fp.JA4R,
				"ja4_o": fp.JA4O, "ja4_ro": fp.JA4RO,
			} {
				if got != "" {
					t.Errorf("%s computed for an oversized hello: %q", field, got)
				}
			}
			if fp.CipherSuites != nil || fp.Extensions != nil || fp.ALPN != nil {
				t.Error("client lists retained for an oversized hello")
			}
			if fp.ServerName != "" {
				t.Error("server name retained for an oversized hello")
			}
		})
	}
}

// A normal hello is untouched by that limit.
func TestNormalHelloIsNotTruncated(t *testing.T) {
	fp := New(curlHello())
	if fp.Truncated {
		t.Error("Truncated = true for an ordinary hello")
	}
	if fp.JA3 == "" || fp.JA4R == "" {
		t.Error("ordinary hello lost its unhashed forms")
	}
}
