package geoip

import (
	"fmt"
	"strings"
)

// Subdivision is one level of administrative division below the country,
// outermost first: for 8.8.8.8 that is California / CA.
type Subdivision struct {
	Name string
	Code string
}

// Record is one provider's answer for one address. Absent values are left at
// their zero value; HasData says whether the provider knew anything at all and
// HasCoordinates distinguishes a missing location from a genuine 0,0.
type Record struct {
	City          string
	Country       string
	CountryCode   string
	Continent     string
	ContinentCode string
	Subdivisions  []Subdivision
	PostalCode    string

	Latitude       float64
	Longitude      float64
	AccuracyRadius uint16
	TimeZone       string
	MetroCode      uint
	HasCoordinates bool

	InEuropeanUnion       bool
	RegisteredCountry     string
	RegisteredCountryCode string

	IsAnycast           bool
	IsAnonymousProxy    bool
	IsSatelliteProvider bool

	ASN    uint
	ASNOrg string

	HasData bool
}

// ASNLabel renders the ASN the way the plain-text output has always shown it:
// "AS15169 (GOOGLE)", or "AS15169" when the organisation is unknown.
func (r Record) ASNLabel() string {
	if r.ASN == 0 {
		return ""
	}
	if r.ASNOrg != "" {
		return fmt.Sprintf("AS%d (%s)", r.ASN, r.ASNOrg)
	}
	return fmt.Sprintf("AS%d", r.ASN)
}

// Region is the outermost subdivision, which is what most callers want.
func (r Record) Region() (name, code string) {
	if len(r.Subdivisions) == 0 {
		return "", ""
	}
	return r.Subdivisions[0].Name, r.Subdivisions[0].Code
}

// Answer pairs a record with the provider that produced it, and what that
// provider covers at all, so an empty field can be read correctly.
type Answer struct {
	Source   string
	Record   Record
	Provides Fields
}

// continentNames decodes the seven continent codes GeoIP data shares. A source
// that reports only the code still gets a name, because the name is the code's
// definition rather than something the source asserted.
var continentNames = map[string]string{
	"AF": "Africa",
	"AN": "Antarctica",
	"AS": "Asia",
	"EU": "Europe",
	"NA": "North America",
	"OC": "Oceania",
	"SA": "South America",
}

// countryCode returns cc upper-cased if it names a country, and "" for the
// codes some datasets put in that field instead. IPFire's data carries "EU" and
// "AP" for allocations made to a whole region, "ZZ" for unknown and zero bytes
// for none; older GeoIP data used "A1"–"A3" and "O1". Reporting any of them as
// a country would place a network somewhere that does not exist.
func countryCode(cc string) string {
	cc = strings.ToUpper(cc)
	if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return ""
	}
	switch cc {
	case "EU", "AP", "ZZ", "XX", "A1", "A2", "A3", "O1":
		return ""
	}
	return cc
}
