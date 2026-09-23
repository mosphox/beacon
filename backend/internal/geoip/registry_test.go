package geoip

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

// fakeProvider answers with a fixed record and reports what Download returns.
type fakeProvider struct {
	name     string
	provides Fields
	download error
	rec      Record

	downloads, opens int
}

func (f *fakeProvider) Name() string              { return f.name }
func (f *fakeProvider) Provides() Fields          { return f.provides }
func (f *fakeProvider) FilesPresent() bool        { return true }
func (f *fakeProvider) Download() error           { f.downloads++; return f.download }
func (f *fakeProvider) Open() error               { f.opens++; return nil }
func (f *fakeProvider) Close()                    {}
func (f *fakeProvider) Lookup(net.IP) Record      { return f.rec }
func (f *fakeProvider) MinRefresh() time.Duration { return 0 }

// runOnce runs a single refresh pass: the loop does its work before it first
// waits, and a cancelled context ends it there.
func runOnce(r *Registry) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.RefreshLoop(ctx)
}

// A source whose published data has not changed has checked out: it is stamped
// so it is not asked again until the next interval, and its open files are
// left alone rather than reopened.
func TestRefreshCountsNotModifiedAsAFreshCheck(t *testing.T) {
	p := &fakeProvider{name: "Fake", download: errNotModified}
	r, err := NewRegistry(t.TempDir(), time.Hour, p)
	if err != nil {
		t.Fatal(err)
	}
	if p.opens != 1 {
		t.Fatalf("opened %d times at start, want 1", p.opens)
	}

	runOnce(r)
	if p.downloads != 1 || p.opens != 1 {
		t.Errorf("after an unchanged check: %d downloads, %d opens; want 1 and 1", p.downloads, p.opens)
	}
	if _, err := os.Stat(r.stampPath(p)); err != nil {
		t.Errorf("no timestamp after an unchanged check: %v", err)
	}

	runOnce(r)
	if p.downloads != 1 {
		t.Errorf("checked again within the interval: %d downloads, want 1", p.downloads)
	}
}

func TestRefreshReopensAfterANewDownload(t *testing.T) {
	p := &fakeProvider{name: "Fake"}
	r, err := NewRegistry(t.TempDir(), time.Hour, p)
	if err != nil {
		t.Fatal(err)
	}
	runOnce(r)
	if p.downloads != 1 || p.opens != 2 {
		t.Errorf("after a download: %d downloads, %d opens; want 1 and 2", p.downloads, p.opens)
	}
}

// What a source covers travels with each answer, so an empty field can be read
// as "no answer" or "never answers" correctly.
func TestLookupAllCarriesWhatEachSourceProvides(t *testing.T) {
	city := &fakeProvider{
		name: "City", provides: FieldCity | FieldCountry,
		rec: Record{City: "Mountain View", CountryCode: "US", HasData: true},
	}
	country := &fakeProvider{
		name: "Country", provides: FieldCountry,
		rec: Record{CountryCode: "US", HasData: true},
	}
	silent := &fakeProvider{name: "Silent", provides: FieldCountry}

	r, err := NewRegistry(t.TempDir(), time.Hour, city, country, silent)
	if err != nil {
		t.Fatal(err)
	}
	answers := r.LookupAll("8.8.8.8")
	if len(answers) != 2 {
		t.Fatalf("got %d answers, want 2 (a source with no data is left out)", len(answers))
	}
	if answers[0].Source != "City" || answers[0].Provides != FieldCity|FieldCountry {
		t.Errorf("first answer = %s/%b", answers[0].Source, answers[0].Provides)
	}
	if answers[1].Source != "Country" || answers[1].Provides != FieldCountry {
		t.Errorf("second answer = %s/%b", answers[1].Source, answers[1].Provides)
	}
}

// Each source declares a subset of the schema, and the real ones say what
// their data actually carries.
func TestProvidersDeclareWhatTheyCover(t *testing.T) {
	for _, tc := range []struct {
		p           Provider
		has, hasNot Fields
	}{
		{NewMaxMind("id", "key", "", nil), AllFields, 0},
		{NewDBIP("", nil), FieldCity | FieldCoordinates | FieldEuropeanUnion, FieldPostalCode | FieldTimeZone | FieldAnycast},
		{NewIPLocate("", nil), FieldCountry | FieldContinent | FieldASNOrg, FieldCity | FieldEuropeanUnion | FieldAnycast},
		{NewIPFire("", nil), FieldCountry | FieldAnycast | FieldSatelliteProvider, FieldCity | FieldEuropeanUnion},
		{NewIPLocationDB("", nil), FieldCountry, FieldContinent | FieldASN},
	} {
		got := tc.p.Provides()
		if got&^AllFields != 0 {
			t.Errorf("%s declares fields outside the schema: %b", tc.p.Name(), got)
		}
		if !got.Has(tc.has) || got&tc.hasNot != 0 {
			t.Errorf("%s provides %b; want all of %b and none of %b", tc.p.Name(), got, tc.has, tc.hasNot)
		}
	}
}
