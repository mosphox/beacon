package config

import "testing"

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
