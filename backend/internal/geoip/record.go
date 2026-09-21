package geoip

import "fmt"

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

// Answer pairs a record with the provider that produced it.
type Answer struct {
	Source string
	Record Record
}
