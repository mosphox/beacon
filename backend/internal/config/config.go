package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultUpdatePeriodHours = 12
	defaultDataDir           = "data"
	defaultListenAddr        = ":8000"
	defaultTLSListenAddr     = ":8443"
	defaultFrontendURL       = "http://frontend:3000"
	defaultRDNSTimeout       = 300 * time.Millisecond
	defaultRDNSCacheTTL      = time.Hour
)

type Config struct {
	// MaxMind credentials are optional: with none set, beacon runs on the
	// sources that need no account.
	MaxMindAccountID  string
	MaxMindLicenseKey string
	DBIPEnabled       bool

	// The other no-account sources, each on unless switched off.
	IPLocateEnabled     bool
	IPFireEnabled       bool
	IPLocationDBEnabled bool

	// RIPE reports the country an address is registered to, never where it
	// is, so on its own it does not count as a GeoIP source.
	RIPEEnabled bool
	// Geofeeds are the operators' own locations, found through RIPE's
	// records, so they need RIPE on; they cover too little to count alone.
	GeofeedsEnabled bool

	// IPinfo Lite needs a free account's token; set, it enables the source.
	IPinfoToken string

	UpdatePeriodHours int
	DataDir           string
	ListenAddr        string

	// FrontendURL is the Next.js upstream browser navigations are proxied to.
	// Empty disables proxying, and browsers then get plain text like anything
	// else — useful when running the backend on its own.
	FrontendURL string

	// TrustProxyHeaders controls whether X-Real-IP / X-Forwarded-For from the
	// caller are believed. It must be false when the backend is exposed
	// directly, or any client can pick its own address.
	TrustProxyHeaders bool

	RDNSEnabled  bool
	RDNSTimeout  time.Duration
	RDNSCacheTTL time.Duration

	TLSEnabled    bool
	TLSListenAddr string
	Domains       []string
	ACMEEmail     string
	ACMECA        string
	CFAPIToken    string
	CFZoneToken   string
	ProxyProtocol bool
}

func Load() (Config, error) {
	cfg := Config{
		MaxMindAccountID:  os.Getenv("MAXMIND_ACCOUNT_ID"),
		MaxMindLicenseKey: os.Getenv("MAXMIND_LICENSE_KEY"),
	}

	// Partial MaxMind credentials are a configuration mistake, not a choice to
	// run without it.
	if (cfg.MaxMindAccountID == "") != (cfg.MaxMindLicenseKey == "") {
		return Config{}, fmt.Errorf("MAXMIND_ACCOUNT_ID and MAXMIND_LICENSE_KEY must be set together")
	}

	var err error
	for _, s := range []struct {
		env string
		dst *bool
	}{
		{"DBIP_ENABLED", &cfg.DBIPEnabled},
		{"IPLOCATE_ENABLED", &cfg.IPLocateEnabled},
		{"IPFIRE_ENABLED", &cfg.IPFireEnabled},
		{"IP_LOCATION_DB_ENABLED", &cfg.IPLocationDBEnabled},
		{"RIPE_ENABLED", &cfg.RIPEEnabled},
		{"GEOFEEDS_ENABLED", &cfg.GeofeedsEnabled},
	} {
		if *s.dst, err = boolEnv(s.env, true); err != nil {
			return Config{}, err
		}
	}
	// The token alone, which is what IPinfo's dashboard shows. It also hands out
	// the whole download URL with the token inside, and that pasted here would
	// otherwise fail at the first download instead of now.
	cfg.IPinfoToken = strings.TrimSpace(os.Getenv("IPINFO_TOKEN"))
	for _, c := range cfg.IPinfoToken {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9') {
			return Config{}, fmt.Errorf("invalid IPINFO_TOKEN: want the token alone, not a URL " +
				"(it is the value after token= in the download link)")
		}
	}

	// RIPE is left out: it places nothing, and a service it alone answered
	// for would report every address as unlocated.
	if !cfg.DBIPEnabled && !cfg.IPLocateEnabled && !cfg.IPFireEnabled && !cfg.IPLocationDBEnabled &&
		cfg.MaxMindAccountID == "" && cfg.IPinfoToken == "" {
		return Config{}, fmt.Errorf("no GeoIP source enabled: set MAXMIND_* credentials or IPINFO_TOKEN, " +
			"or leave one of DBIP_ENABLED, IPLOCATE_ENABLED, IPFIRE_ENABLED, IP_LOCATION_DB_ENABLED on")
	}

	cfg.UpdatePeriodHours = defaultUpdatePeriodHours
	if v := os.Getenv("GEOIP_UPDATE_INTERVAL_HOURS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid GEOIP_UPDATE_INTERVAL_HOURS %q: %w", v, err)
		}
		cfg.UpdatePeriodHours = n
	}
	if cfg.UpdatePeriodHours <= 0 {
		return Config{}, fmt.Errorf("GEOIP_UPDATE_INTERVAL_HOURS must be positive, got %d", cfg.UpdatePeriodHours)
	}

	cfg.DataDir = envOr("DATA_DIR", defaultDataDir)
	// LookupEnv, not envOr: an explicitly empty LISTEN_ADDR is how a TLS-only
	// deployment turns the plain listener off, and envOr would have substituted
	// the default back in — which made that unreachable and left main's
	// "nothing to listen on" check dead code.
	if v, ok := os.LookupEnv("LISTEN_ADDR"); ok {
		cfg.ListenAddr = v
	} else {
		cfg.ListenAddr = defaultListenAddr
	}
	cfg.TLSListenAddr = envOr("TLS_LISTEN_ADDR", defaultTLSListenAddr)

	frontendURL, ok := os.LookupEnv("FRONTEND_URL")
	if !ok {
		frontendURL = defaultFrontendURL
	}
	if frontendURL != "" {
		u, err := url.Parse(frontendURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return Config{}, fmt.Errorf("invalid FRONTEND_URL %q: must be an absolute http(s) URL", frontendURL)
		}
	}
	cfg.FrontendURL = frontendURL

	// Defaults to false. Believing a client's own X-Forwarded-For is only safe
	// where something in front is guaranteed to overwrite it, and a default
	// that assumes so makes the one thing this service reports falsifiable by
	// anyone who sends a header.
	if cfg.TrustProxyHeaders, err = boolEnv("TRUST_PROXY_HEADERS", false); err != nil {
		return Config{}, err
	}
	if cfg.RDNSEnabled, err = boolEnv("RDNS_ENABLED", true); err != nil {
		return Config{}, err
	}
	if cfg.RDNSTimeout, err = durationEnv("RDNS_TIMEOUT_MS", time.Millisecond, defaultRDNSTimeout); err != nil {
		return Config{}, err
	}
	if cfg.RDNSCacheTTL, err = durationEnv("RDNS_CACHE_TTL_SECONDS", time.Second, defaultRDNSCacheTTL); err != nil {
		return Config{}, err
	}

	if err := loadTLS(&cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func loadTLS(cfg *Config) error {
	var err error
	if cfg.TLSEnabled, err = boolEnv("TLS_ENABLED", false); err != nil {
		return err
	}
	if cfg.ProxyProtocol, err = boolEnv("PROXY_PROTOCOL", false); err != nil {
		return err
	}
	if !cfg.TLSEnabled {
		return nil
	}

	for _, d := range strings.Split(os.Getenv("DOMAIN"), ",") {
		if d = strings.TrimSpace(d); d != "" {
			cfg.Domains = append(cfg.Domains, d)
		}
	}
	if len(cfg.Domains) == 0 {
		return fmt.Errorf("TLS_ENABLED is set but DOMAIN is empty")
	}

	cfg.CFAPIToken = os.Getenv("CF_API_TOKEN")
	if cfg.CFAPIToken == "" {
		return fmt.Errorf("TLS_ENABLED is set but CF_API_TOKEN is empty")
	}
	cfg.CFZoneToken = os.Getenv("CF_ZONE_TOKEN")
	cfg.ACMEEmail = os.Getenv("ACME_EMAIL")
	cfg.ACMECA = os.Getenv("ACME_CA")

	return nil
}

func (c Config) MaxMindEnabled() bool { return c.MaxMindAccountID != "" }

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func boolEnv(name string, def bool) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q: want true or false", name, v)
	}
	return b, nil
}

func durationEnv(name string, unit, def time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid %s %q: want a positive integer", name, v)
	}
	return time.Duration(n) * unit, nil
}
