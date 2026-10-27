package geo

import (
	"strings"
	"testing"
)

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"Adıyaman Province":         "Adıyaman",
		"Kadıköy İlçesi":            "Kadıköy",
		"Beşiktaş ilçesi":           "Beşiktaş",
		"Cook County":               "Cook",
		"Foo District Municipality": "Foo",
		"Al Jawf Governorate":       "Al Jawf",
		"State of Alabama":          "Alabama",
		"Région Bretagne":           "Bretagne",
		"Fukushima-ken":             "Fukushima",
		"District":                  "District",
		"Rhode Island":              "Rhode Island",
		"Quezon City":               "Quezon City",
		"Washington, D.C.":          "Washington, D.C.",
		"  Padded   Region ":        "Padded",
	} {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuildNames checks that a place is named in its country's language when GeoNames has such a name in Latin script, else by its own name, else by
// its English name, and that the suffix words go.
func TestBuildNames(t *testing.T) {
	// alternatenameid geonameid isolanguage name isPreferred isShort isColloquial isHistoric
	alt := "1\t745042\ttr\tİstanbul\t1\t\t\t\n" +
		"2\t745042\ten\tIstanbul Province\t1\t\t\t\n" +
		"3\t323784\ttr\tAnk\t\t1\t\t\n" + // short: never taken
		"4\t323784\ttr\tAnkara Ili\t\t\t\t\n" +
		"5\t2950157\ten\tBerlin State\t1\t\t\t\n"
	src := Sources{
		CountryInfo: strings.NewReader(countryInfo),
		Admin1:      strings.NewReader("TR.34\tIstanbul Province\tIstanbul Province\t745042\nTR.06\tAnkara Province\tAnkara Province\t323784\nDE.16\tBerlin\tBerlin\t2950157\n"),
		Admin2:      strings.NewReader("TR.34.2\tBeşiktaş İlçesi\tBesiktas Ilcesi\t12\n"),
		Alternates:  strings.NewReader(alt),
	}
	d, err := Build("test", src)
	if err != nil {
		t.Fatal(err)
	}
	tr, _ := d.RegionsOf("TR")
	got := []string{}
	for _, r := range tr {
		got = append(got, r.Name+"/"+r.ASCIIName)
	}
	if want := "Ankara Ili/Ankara,İstanbul/Istanbul"; strings.Join(got, ",") != want {
		t.Errorf("TR regions %v, want %s", got, want)
	}
	// GeoNames' own name is Latin, so the English alternate is not preferred over it.
	if de, _ := d.RegionsOf("DE"); len(de) != 1 || de[0].Name != "Berlin" {
		t.Errorf("DE regions %+v", de)
	}
	if ds, _ := d.DistrictsOf("TR.34"); len(ds) != 1 || ds[0].Name != "Beşiktaş" || ds[0].ASCIIName != "Besiktas" {
		t.Errorf("TR.34 districts %+v", ds)
	}
}

// TestEmbeddedNamesHaveNoSuffix is the guard of the shipped file: no region or district name (nor its ASCII name) ends with a generic word such as
// "Province" or "İlçesi", or starts with one such as "State of". Regenerate with cmd/geoimport if this fails after a change of the suffix list.
func TestEmbeddedNamesHaveNoSuffix(t *testing.T) {
	d, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	check := func(kind, id, name string) {
		if CleanName(name) != name {
			t.Errorf("%s %s: %q is not clean (%q)", kind, id, name, CleanName(name))
		}
	}
	for _, rs := range d.Regions {
		for _, r := range rs {
			check("region", r.ID, r.Name)
			check("region", r.ID, r.ASCIIName)
		}
	}
	for _, ds := range d.Districts {
		for _, x := range ds {
			check("district", x.ID, x.Name)
			check("district", x.ID, x.ASCIIName)
		}
	}
}
