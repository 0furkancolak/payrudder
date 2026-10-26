package geo

import (
	"bytes"
	"strings"
	"testing"
)

const (
	countryInfo = "#ISO\tISO3\tISO-Numeric\tfips\tCountry\tCapital\tArea\tPopulation\tContinent\ttld\tCurrencyCode\tCurrencyName\tPhone\tPostal Code Format\tPostal Code Regex\tLanguages\tgeonameid\tneighbours\tEquivalentFipsCode\n" +
		"TR\tTUR\t792\tTU\tTurkey\tAnkara\t780580\t85000000\tAS\t.tr\tTRY\tLira\t90\t#####\t^(\\d{5})$\ttr-TR,ku\t298795\tSY,GE\t\n" +
		"DE\tDEU\t276\tGM\tGermany\tBerlin\t357021\t83000000\tEU\t.de\tEUR\tEuro\t49\t#####\t^(\\d{5})$\tde\t2921044\tCH\t\n" +
		"CS\tSCG\t891\tYI\tSerbia and Montenegro\tBelgrade\t102350\t10829175\tEU\t.cs\tRSD\tDinar\t381\t#####\t^(\\d{5})$\tcu,hu,sq,sr\t8505033\tAL\t\n" +
		"MC\tMCO\t492\tMN\tMonaco\tMonaco\t1.95\t38000\tEU\t.mc\tEUR\tEuro\t377\t#####\t^(\\d{5})$\tfr-MC\t2993457\tFR\t\n"
	admin1 = "TR.34\tIstanbul\tIstanbul\t745042\nTR.06\tAnkara\tAnkara\t323784\nDE.16\tBerlin\tBerlin\t2950157\nXX.01\tNowhere\tNowhere\t1\n"
	admin2 = "TR.34.1\tKadıköy\tKadikoy\t11\nTR.34.2\tBeşiktaş\tBesiktas\t12\nTR.99.3\tOrphan\tOrphan\t13\nDE.16.00\tBerlin\tBerlin\t14\n"
	// geonameid name asciiname alternatenames lat lon class code country cc2 admin1 ...
	cities = "21\tKadıköy\tKadikoey\t\t0\t0\tP\tPPLX\tTR\t\t34\t\t\t\t500000\n" +
		"22\tÜsküdar\tUskudar\t\t0\t0\tP\tPPLX\tTR\t\t34\t\t\t\t500000\n" +
		"23\tÇankaya\tCankaya\t\t0\t0\tP\tPPLA2\tTR\t\t06\t\t\t\t900000\n" +
		"24\tMonaco\tMonaco\t\t0\t0\tP\tPPLC\tMC\t\t00\t\t\t\t38000\n"
)

func fixture(t *testing.T) *Dataset {
	t.Helper()
	d, err := Build("test", Sources{CountryInfo: strings.NewReader(countryInfo), Admin1: strings.NewReader(admin1), Admin2: strings.NewReader(admin2), Cities: strings.NewReader(cities)})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBuild(t *testing.T) {
	d := fixture(t)
	var codes []string
	for _, c := range d.Countries {
		codes = append(codes, c.ISO2)
	}
	if got := strings.Join(codes, ","); got != "DE,MC,TR" {
		t.Fatalf("countries %s (sorted, dissolved CS left out)", got)
	}
	if c, _ := d.Country("tr"); c.Name != "Turkey" || c.ISO3 != "TUR" || c.Continent != "AS" || c.GeonameID != 298795 {
		t.Errorf("TR: %+v", c)
	}
	regions, ok := d.RegionsOf("TR")
	if !ok || len(regions) != 2 || regions[0].ID != "TR.06" || regions[1].Name != "Istanbul" || regions[1].Code != "34" {
		t.Fatalf("TR regions: %+v", regions)
	}
	if rs, ok := d.RegionsOf("MC"); !ok || len(rs) != 0 {
		t.Errorf("a country without regions exists with none: %v %v", rs, ok)
	}
	if _, ok := d.RegionsOf("XX"); ok {
		t.Error("a region of an unknown country must not make the country")
	}
	ist, ok := d.DistrictsOf("TR.34")
	if !ok {
		t.Fatal("TR.34")
	}
	var names []string
	for _, x := range ist {
		names = append(names, x.ASCIIName+":"+x.Kind)
	}
	// Kadıköy is a division and a town: listed once, as the division. Sorted by the ASCII name.
	if got := strings.Join(names, ","); got != "Besiktas:admin2,Kadikoy:admin2,Uskudar:city" {
		t.Fatalf("Istanbul districts: %s", got)
	}
	if ds, _ := d.DistrictsOf("TR.06"); len(ds) != 1 || ds[0].ID != "23" || ds[0].Kind != KindCity {
		t.Errorf("Ankara: %+v", ds)
	}
	if _, ok := d.DistrictsOf("TR.99"); ok {
		t.Error("TR.99 is no region")
	}
	if _, ok := d.DistrictsOf("tr.34"); !ok {
		t.Error("the region id is case-insensitive")
	}
}

func TestEncodeIsStableAndRoundTrips(t *testing.T) {
	var a, b bytes.Buffer
	if err := Encode(&a, fixture(t)); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&b, fixture(t)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("the same input must give the same file")
	}
	d, err := Decode(bytes.NewReader(a.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if ds, _ := d.DistrictsOf("TR.34"); len(ds) != 3 {
		t.Fatalf("round trip: %+v", ds)
	}
}

// TestEmbeddedData checks the shipped file: every country, the regions of a few, the districts of a few regions, and its size.
func TestEmbeddedData(t *testing.T) {
	if len(embedded) > 5<<20 {
		t.Errorf("data/geonames.json.gz is %d bytes; keep it under 5 MiB", len(embedded))
	}
	d, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Countries) < 240 || !strings.HasPrefix(d.Version, "geonames-") {
		t.Fatalf("%d countries, version %q", len(d.Countries), d.Version)
	}
	for i := 1; i < len(d.Countries); i++ {
		if d.Countries[i-1].ISO2 >= d.Countries[i].ISO2 {
			t.Fatalf("countries not sorted at %s", d.Countries[i].ISO2)
		}
	}
	for iso2, min := range map[string]int{"TR": 81, "US": 50, "DE": 16, "FR": 13, "ES": 17, "GB": 4, "JP": 47} {
		if rs, _ := d.RegionsOf(iso2); len(rs) < min {
			t.Errorf("%s has %d regions, want at least %d", iso2, len(rs), min)
		}
	}
	for region, place := range map[string]string{"TR.34": "Kadıköy", "TR.68": "Çankaya", "US.CA": "Los Angeles", "DE.16": "Berlin", "FR.11": "Paris"} {
		ds, ok := d.DistrictsOf(region)
		if !ok {
			t.Errorf("%s missing", region)
			continue
		}
		found := false
		for _, x := range ds {
			if x.Name == place {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no %s among %d districts", region, place, len(ds))
		}
	}
}
