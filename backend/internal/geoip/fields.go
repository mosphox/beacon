package geoip

// Fields is the set of Record fields a provider can fill at all.
//
// A Record's zero values cannot tell "this source had no postal code for this
// address" from "this source has no postal codes": DB-IP Lite ships none, and a
// country-only database has no city to give for any address. Both read as
// empty. Comparing sources needs the difference. An absence from a source that
// could have answered is information; an absence from one that never answers
// that question is not, and marking it as a disagreement would bury the real
// ones. So each provider declares what it covers.
type Fields uint32

const (
	FieldCity Fields = 1 << iota
	FieldRegion
	FieldPostalCode
	FieldCoordinates
	FieldAccuracyRadius
	FieldTimeZone
	FieldMetroCode
	FieldCountry
	FieldContinent
	FieldEuropeanUnion
	FieldRegisteredCountry
	FieldASN
	FieldASNOrg
	FieldAnycast
	FieldAnonymousProxy
	FieldSatelliteProvider

	// AllFields is every field above.
	AllFields = FieldSatelliteProvider<<1 - 1
)

// Has reports whether every field in want is covered.
func (f Fields) Has(want Fields) bool { return f&want == want }
