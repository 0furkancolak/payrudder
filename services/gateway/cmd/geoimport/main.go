// Command geoimport makes the gateway's embedded list of countries, regions and districts (internal/geo/data/geonames.json.gz) from the GeoNames dumps.
//
//	go run ./cmd/geoimport                       # download the dumps and write the file (run in services/gateway)
//	go run ./cmd/geoimport -src /path/to/dumps   # use files already downloaded (countryInfo.txt, admin1CodesASCII.txt, admin2Codes.txt, cities15000.zip)
//
// The same dumps and -version give the same file byte for byte. GeoNames data is CC BY 4.0: see docs/geo-data.md.
package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/0furkancolak/payrudder/services/gateway/internal/geo"
)

const baseURL = "https://download.geonames.org/export/dump/"

func main() {
	src := flag.String("src", "", "directory holding the dump files; empty downloads them from "+baseURL)
	out := flag.String("out", "internal/geo/data/geonames.json.gz", "file to write")
	version := flag.String("version", "geonames-"+time.Now().UTC().Format("2006-01-02"), "name of the export, stored in the file")
	noCities := flag.Bool("no-cities", false, "leave out the towns of cities15000 (districts are then the second-level divisions only)")
	flag.Parse()
	if err := run(*src, *out, *version, !*noCities); err != nil {
		fmt.Fprintln(os.Stderr, "geoimport:", err)
		os.Exit(1)
	}
}

func run(src, out, version string, withCities bool) error {
	get := func(name string) ([]byte, error) { return fetch(src, name) }
	countries, err := get("countryInfo.txt")
	if err != nil {
		return err
	}
	admin1, err := get("admin1CodesASCII.txt")
	if err != nil {
		return err
	}
	admin2, err := get("admin2Codes.txt")
	if err != nil {
		return err
	}
	s := geo.Sources{CountryInfo: bytes.NewReader(countries), Admin1: bytes.NewReader(admin1), Admin2: bytes.NewReader(admin2)}
	if withCities {
		z, err := get("cities15000.zip")
		if err != nil {
			return err
		}
		cities, err := unzipOne(z, "cities15000.txt")
		if err != nil {
			return err
		}
		s.Cities = bytes.NewReader(cities)
	}
	d, err := geo.Build(version, s)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := geo.Encode(&buf, d); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // a directory of the source tree
		return err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil { //nolint:gosec // a committed source file
		return err
	}
	regions, districts := 0, 0
	for _, r := range d.Regions {
		regions += len(r)
	}
	for _, x := range d.Districts {
		districts += len(x)
	}
	fmt.Fprintf(os.Stdout, "wrote %s: %d countries, %d regions, %d districts, %d bytes\n", out, len(d.Countries), regions, districts, buf.Len())
	return nil
}

// fetch reads a dump from the directory, or downloads it.
func fetch(dir, name string) ([]byte, error) {
	if dir != "" {
		return os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // the dump directory the person named
	}
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Get(baseURL + name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", name, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

func unzipOne(b []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(io.LimitReader(rc, 256<<20))
	}
	return nil, fmt.Errorf("%s not in the archive", name)
}
