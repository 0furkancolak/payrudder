package geo

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Sources are the GeoNames dump files Build reads (https://download.geonames.org/export/dump/). Cities may be nil: the districts are then the
// second-level divisions only.
type Sources struct {
	CountryInfo io.Reader // countryInfo.txt
	Admin1      io.Reader // admin1CodesASCII.txt
	Admin2      io.Reader // admin2Codes.txt
	Cities      io.Reader // cities15000.txt (unzipped)
}

// dissolved are countries GeoNames still lists that no longer exist and that no new address names.
var dissolved = map[string]bool{"CS": true, "AN": true}

// Build turns the GeoNames dumps into a dataset: every country, every region, and under each region its second-level divisions plus the towns of the
// cities file that no division of the same name already covers. Every list is sorted (countries by code, the rest by name) so the output is stable.
func Build(version string, src Sources) (*Dataset, error) {
	d := &Dataset{Version: version, Regions: map[string][]Region{}, Districts: map[string][]District{}}
	if err := eachRow(src.CountryInfo, func(f []string) error {
		if len(f) < 17 || len(f[0]) != 2 || dissolved[f[0]] {
			return nil
		}
		id, _ := strconv.Atoi(f[16])
		d.Countries = append(d.Countries, Country{ISO2: f[0], ISO3: f[1], Name: f[4], Continent: f[8], GeonameID: id})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("countryInfo: %w", err)
	}
	slices.SortFunc(d.Countries, func(a, b Country) int { return strings.Compare(a.ISO2, b.ISO2) })
	known := map[string]bool{}
	for _, c := range d.Countries {
		known[c.ISO2] = true
	}

	regionIDs := map[string]bool{}
	if err := eachRow(src.Admin1, func(f []string) error {
		if len(f) < 4 {
			return nil
		}
		country, code, ok := strings.Cut(f[0], ".")
		if !ok || !known[country] || code == "" {
			return nil
		}
		id, _ := strconv.Atoi(f[3])
		d.Regions[country] = append(d.Regions[country], Region{ID: f[0], Code: code, Name: f[1], ASCIIName: f[2], GeonameID: id})
		regionIDs[f[0]] = true
		return nil
	}); err != nil {
		return nil, fmt.Errorf("admin1: %w", err)
	}

	// A place is listed once per region: a town is left out when a division already has its name or its ASCII name (the two files transliterate
	// differently, "Kadikoey" and "Kadikoy" for Kadıköy, so either matching is enough).
	seen := map[string]map[string]bool{} // region id → lower-case names already listed
	add := func(region string, dist District) {
		names := []string{strings.ToLower(strings.TrimSpace(dist.Name)), fold(dist.ASCIIName, dist.Name)}
		if names[0] == "" {
			return
		}
		if seen[region] == nil {
			seen[region] = map[string]bool{}
		}
		if seen[region][names[0]] || seen[region][names[1]] {
			return
		}
		seen[region][names[0]], seen[region][names[1]] = true, true
		d.Districts[region] = append(d.Districts[region], dist)
	}
	if err := eachRow(src.Admin2, func(f []string) error {
		if len(f) < 3 {
			return nil
		}
		parts := strings.SplitN(f[0], ".", 3)
		if len(parts) != 3 {
			return nil
		}
		region := parts[0] + "." + parts[1]
		if !regionIDs[region] {
			return nil
		}
		add(region, District{ID: f[0], Name: f[1], ASCIIName: f[2], Kind: KindAdmin2})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("admin2: %w", err)
	}
	if src.Cities != nil {
		if err := eachRow(src.Cities, func(f []string) error {
			if len(f) < 11 {
				return nil
			}
			region := f[8] + "." + f[10]
			if !regionIDs[region] {
				return nil
			}
			add(region, District{ID: f[0], Name: f[1], ASCIIName: f[2], Kind: KindCity})
			return nil
		}); err != nil {
			return nil, fmt.Errorf("cities: %w", err)
		}
	}

	for _, rs := range d.Regions {
		slices.SortFunc(rs, func(a, b Region) int { return compareNames(a.ASCIIName, a.Name, a.ID, b.ASCIIName, b.Name, b.ID) })
	}
	for _, ds := range d.Districts {
		slices.SortFunc(ds, func(a, b District) int { return compareNames(a.ASCIIName, a.Name, a.ID, b.ASCIIName, b.Name, b.ID) })
	}
	return d, nil
}

// eachRow calls fn with the tab-separated fields of every line that is not empty or a # comment.
func eachRow(r io.Reader, fn func([]string) error) error {
	if r == nil {
		return fmt.Errorf("missing file")
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := fn(strings.Split(line, "\t")); err != nil {
			return err
		}
	}
	return sc.Err()
}

// fold is the name two places are the same by: the ASCII name (or the name) in lower case.
func fold(ascii, name string) string {
	if ascii == "" {
		ascii = name
	}
	return strings.ToLower(strings.TrimSpace(ascii))
}

func compareNames(aASCII, aName, aID, bASCII, bName, bID string) int {
	if c := strings.Compare(fold(aASCII, aName), fold(bASCII, bName)); c != 0 {
		return c
	}
	if c := strings.Compare(aName, bName); c != 0 {
		return c
	}
	return strings.Compare(aID, bID)
}
