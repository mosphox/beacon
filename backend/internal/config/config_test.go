package config

import (
	"strings"
	"testing"
)

// LISTEN_ADDR set to empty is how a TLS-only deployment turns the plain
// listener off. envOr used to treat empty as unset and substitute the default
// back in, which made that unreachable — and made main's "nothing to listen on"
// check dead code.
func TestEmptyListenAddrDisablesThePlainListener(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != "" {
		t.Errorf("ListenAddr = %q for an explicitly empty LISTEN_ADDR, want %q", cfg.ListenAddr, "")
	}
}

func TestUnsetListenAddrGetsTheDefault(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != defaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, defaultListenAddr)
	}
}

// The default decides whether a stranger can choose the address this service
// reports about them. It must be off unless an operator turns it on.
func TestProxyHeadersAreNotTrustedByDefault(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TrustProxyHeaders {
		t.Error("TrustProxyHeaders = true by default; a client could pick its own address")
	}
}

func TestTrustProxyHeadersIsOptIn(t *testing.T) {
	t.Setenv("TRUST_PROXY_HEADERS", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.TrustProxyHeaders {
		t.Error("TrustProxyHeaders = false with TRUST_PROXY_HEADERS=true")
	}
}

// Every source that needs no account is on unless switched off.
func TestNoAccountSourcesAreOnByDefault(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.DBIPEnabled || !cfg.IPLocateEnabled || !cfg.IPFireEnabled || !cfg.IPLocationDBEnabled || !cfg.RIPEEnabled {
		t.Errorf("defaults: DB-IP %v, IPLocate %v, IPFire %v, ip-location-db %v, RIPE %v; want all on",
			cfg.DBIPEnabled, cfg.IPLocateEnabled, cfg.IPFireEnabled, cfg.IPLocationDBEnabled, cfg.RIPEEnabled)
	}

	t.Setenv("RIPE_ENABLED", "false")
	if cfg, err := Load(); err != nil || cfg.RIPEEnabled {
		t.Errorf("RIPE_ENABLED=false: RIPE %v, err %v", cfg.RIPEEnabled, err)
	}
}

func TestSwitchingEverySourceOffIsRefused(t *testing.T) {
	for _, env := range []string{"DBIP_ENABLED", "IPLOCATE_ENABLED", "IPFIRE_ENABLED", "IP_LOCATION_DB_ENABLED"} {
		t.Setenv(env, "false")
	}
	// RIPE is still on, and does not count: it locates nothing.
	if _, err := Load(); err == nil {
		t.Fatal("Load succeeded with no GeoIP source at all")
	}

	// Any one source is enough.
	t.Setenv("IPFIRE_ENABLED", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with only IPFire on: %v", err)
	}
	if cfg.DBIPEnabled || cfg.IPLocateEnabled || !cfg.IPFireEnabled || cfg.IPLocationDBEnabled {
		t.Errorf("got DB-IP %v, IPLocate %v, IPFire %v, ip-location-db %v",
			cfg.DBIPEnabled, cfg.IPLocateEnabled, cfg.IPFireEnabled, cfg.IPLocationDBEnabled)
	}
}

func TestSourceSwitchesRejectNonsense(t *testing.T) {
	t.Setenv("IPLOCATE_ENABLED", "sometimes")
	if _, err := Load(); err == nil {
		t.Error("Load accepted IPLOCATE_ENABLED=sometimes")
	}
}

func TestIPinfoTokenEnablesTheSource(t *testing.T) {
	t.Setenv("IPINFO_TOKEN", " abc123def456gh ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.IPinfoToken != "abc123def456gh" {
		t.Errorf("IPinfoToken = %q, want it trimmed", cfg.IPinfoToken)
	}
}

// The dashboard hands out the whole download URL; pasted here it must fail at
// startup, with a message saying what to put instead.
func TestIPinfoTokenRefusesAURL(t *testing.T) {
	t.Setenv("IPINFO_TOKEN", "https://ipinfo.io/data/ipinfo_lite.mmdb?token=abc123")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "token=") {
		t.Errorf("Load() error = %v, want one naming token=", err)
	}
}

func TestIPinfoAloneIsEnough(t *testing.T) {
	for _, env := range []string{"DBIP_ENABLED", "IPLOCATE_ENABLED", "IPFIRE_ENABLED", "IP_LOCATION_DB_ENABLED"} {
		t.Setenv(env, "false")
	}
	t.Setenv("IPINFO_TOKEN", "abc123")
	if _, err := Load(); err != nil {
		t.Errorf("Load with only IPinfo: %v", err)
	}
}
