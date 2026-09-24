// Command mmdbgen writes the small .mmdb fixtures the geoip tests read.
//
// It is its own module so that mmdbwriter and what it pulls in never enter
// beacon's go.mod for the sake of a test. Regenerate with:
//
//	cd backend/internal/geoip/testdata/mmdbgen && go run .
//
// The records mirror the shapes the real files use, checked against live
// downloads: IPLocate's and IPinfo's records are flat maps, not the nested
// GeoIP2 layout, and IPLocate's "asn" is a decimal string.
package main

import (
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

type entry struct {
	cidr   string
	record mmdbtype.Map
}

func main() {
	write("iplocate-country.mmdb", []entry{
		{"8.8.8.0/24", mmdbtype.Map{
			"country_code":   mmdbtype.String("US"),
			"country_name":   mmdbtype.String("United States"),
			"continent_code": mmdbtype.String("NA"),
		}},
		{"2001:4860::/32", mmdbtype.Map{
			"country_code":   mmdbtype.String("US"),
			"country_name":   mmdbtype.String("United States"),
			"continent_code": mmdbtype.String("NA"),
		}},
		// A regional placeholder where a country belongs.
		{"203.0.113.0/24", mmdbtype.Map{
			"country_code":   mmdbtype.String("EU"),
			"country_name":   mmdbtype.String("Europe"),
			"continent_code": mmdbtype.String("EU"),
		}},
	})

	write("iplocate-asn.mmdb", []entry{
		{"8.8.8.0/24", mmdbtype.Map{
			"asn":          mmdbtype.String("15169"),
			"org":          mmdbtype.String("Google LLC"),
			"name":         mmdbtype.String("GOOGLE"),
			"domain":       mmdbtype.String("google.com"),
			"country_code": mmdbtype.String("US"),
			"network":      mmdbtype.String("8.8.8.0/24"),
		}},
		// No organisation: the registry handle stands in.
		{"1.1.1.0/24", mmdbtype.Map{
			"asn":  mmdbtype.String("13335"),
			"org":  mmdbtype.String(""),
			"name": mmdbtype.String("CLOUDFLARENET"),
		}},
	})

	// IPinfo Lite: names as well as codes, and an "AS"-prefixed number that
	// about a fifth of networks lack.
	write("ipinfo-lite.mmdb", []entry{
		{"8.8.8.0/24", mmdbtype.Map{
			"country":        mmdbtype.String("United States"),
			"country_code":   mmdbtype.String("US"),
			"continent":      mmdbtype.String("North America"),
			"continent_code": mmdbtype.String("NA"),
			"asn":            mmdbtype.String("AS15169"),
			"as_name":        mmdbtype.String("Google LLC"),
			"as_domain":      mmdbtype.String("google.com"),
		}},
		{"198.51.100.0/24", mmdbtype.Map{
			"country":        mmdbtype.String("Georgia"),
			"country_code":   mmdbtype.String("GE"),
			"continent":      mmdbtype.String("Asia"),
			"continent_code": mmdbtype.String("AS"),
		}},
	})
}

func write(name string, entries []entry) {
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType:            "beacon-test",
		IPVersion:               6,
		RecordSize:              24,
		IncludeReservedNetworks: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range entries {
		_, network, err := net.ParseCIDR(e.cidr)
		if err != nil {
			log.Fatal(err)
		}
		if err := tree.Insert(network, e.record); err != nil {
			log.Fatalf("%s %s: %v", name, e.cidr, err)
		}
	}

	f, err := os.Create(filepath.Join("..", name))
	if err != nil {
		log.Fatal(err)
	}
	if _, err := tree.WriteTo(f); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
}
