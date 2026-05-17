package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	countryEdition = "GeoLite2-Country"
	cityEdition    = "GeoLite2-City"
	asnEdition     = "GeoLite2-ASN"

	downloadBaseURL = "https://download.maxmind.com/app/geoip_download"

	defaultUpdatePeriodHours = 12
	defaultDataDir           = "data"
	defaultListenAddr        = ":8000"
)

type Config struct {
	AccountID         string
	LicenseKey        string
	UpdatePeriodHours int
	DataDir           string
	ListenAddr        string
}

func Load() (Config, error) {
	accountID, err := requireEnv("MAXMIND_ACCOUNT_ID")
	if err != nil {
		return Config{}, err
	}
	licenseKey, err := requireEnv("MAXMIND_LICENSE_KEY")
	if err != nil {
		return Config{}, err
	}

	updatePeriod := defaultUpdatePeriodHours
	if v := os.Getenv("GEOIP_UPDATE_INTERVAL_HOURS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid GEOIP_UPDATE_INTERVAL_HOURS %q: %w", v, err)
		}
		updatePeriod = n
	}
	if updatePeriod <= 0 {
		return Config{}, fmt.Errorf("GEOIP_UPDATE_INTERVAL_HOURS must be positive, got %d", updatePeriod)
	}

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	return Config{
		AccountID:         accountID,
		LicenseKey:        licenseKey,
		UpdatePeriodHours: updatePeriod,
		DataDir:           dataDir,
		ListenAddr:        listenAddr,
	}, nil
}

func requireEnv(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("required environment variable %q is not set", name)
	}
	return v, nil
}

func (c Config) CountryDBPath() string { return filepath.Join(c.DataDir, countryEdition+".mmdb") }
func (c Config) CityDBPath() string    { return filepath.Join(c.DataDir, cityEdition+".mmdb") }
func (c Config) ASNDBPath() string     { return filepath.Join(c.DataDir, asnEdition+".mmdb") }
func (c Config) TimestampPath() string { return filepath.Join(c.DataDir, ".timestamp") }

func (c Config) DBPaths() []string {
	return []string{c.CountryDBPath(), c.CityDBPath(), c.ASNDBPath()}
}

func (c Config) DownloadURLs() []string {
	editions := []string{countryEdition, cityEdition, asnEdition}
	urls := make([]string, 0, len(editions))
	for _, e := range editions {
		urls = append(urls, fmt.Sprintf("%s?edition_id=%s&license_key=%s&suffix=tar.gz", downloadBaseURL, e, c.LicenseKey))
	}
	return urls
}
