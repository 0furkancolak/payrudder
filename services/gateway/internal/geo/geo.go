// Package geo is the reference list of countries, their first-level divisions (regions: states, provinces, counties) and the places under each region
// (districts: second-level divisions, and the larger towns of regions that have none), as GeoNames publishes them. The console offers them wherever it
// asks for an address; an address still stores the country code and the region and city as text, so nothing here decides what an address may say.
//
// The lists are read from nafru-geo (https://geo.nafru.com), the GeoNames service the nafru apps share, with this gateway's key, and kept in memory for a
// day. When nafru-geo cannot be reached the last copy is served; without any copy the call fails. See docs/geo-data.md.
// GeoNames data is licensed under CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/); see docs/geo-data.md for the attribution.
package geo

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Attribution is the credit the licence asks for, sent with every answer that carries the data.
const Attribution = "Data from GeoNames (https://www.geonames.org), licensed under CC BY 4.0"

// Version names the source of the lists in every answer.
const Version = "nafru-geo"

// TTL is how long a list is kept before it is read again; RetryAfter is how long an older copy is served after a failed read before the next try.
const (
	TTL        = 24 * time.Hour
	RetryAfter = time.Minute
)

// Country is one country (ISO 3166-1, plus XK for Kosovo, as GeoNames lists it). The name is English; the console shows the name in the page's language.
type Country struct {
	ISO2      string `json:"iso2"`
	ISO3      string `json:"iso3"`
	Name      string `json:"name"`
	Continent string `json:"continent"`
	GeonameID int    `json:"geoname_id"`
}

// Region is a first-level division of a country. ID is GeoNames' admin1 code ("TR.34"); Code is the part after the country ("34").
type Region struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	ASCIIName string `json:"ascii_name"`
	GeonameID int    `json:"geoname_id"`
}

// Kinds of district.
const (
	KindAdmin2 = "admin2" // a second-level division (a district, county or municipality)
	KindCity   = "city"   // a larger town of a region that has no second-level divisions
)

// District is a place under a region: a second-level division (ID is its admin2 code, "TR.34.1234") or a town (ID is its GeoNames id).
type District struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ASCIIName string `json:"ascii_name"`
	Kind      string `json:"kind"`
}

// ErrUnavailable is returned when nafru-geo cannot be read and no earlier copy is kept.
var ErrUnavailable = errors.New("geo: nafru-geo unavailable")

// Client reads the lists from nafru-geo. It is safe for concurrent use.
type Client struct {
	base string
	key  string
	http *http.Client
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	body    []byte
	fetched time.Time
}

// New is a client for the nafru-geo at base ("https://geo.nafru.com") with the key nafru-geo gave this gateway. A nil hc uses a client with a 10 s timeout.
func New(base, key string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{base: strings.TrimRight(base, "/"), key: key, http: hc, now: time.Now, cache: map[string]entry{}}
}

type place struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	ISO3      string `json:"iso3"`
	Continent string `json:"continent"`
	ASCIIName string `json:"asciiName"`
	GeonameID int    `json:"geonameId"`
	Kind      string `json:"kind"`
}

// Countries returns every country, sorted by ISO2 code.
func (c *Client) Countries(ctx context.Context) ([]Country, error) {
	items, err := c.list(ctx, "/countries")
	if err != nil {
		return nil, err
	}
	out := make([]Country, 0, len(items))
	for _, p := range items {
		out = append(out, Country{ISO2: p.Code, ISO3: p.ISO3, Name: p.Name, Continent: p.Continent, GeonameID: p.GeonameID})
	}
	slices.SortFunc(out, func(a, b Country) int { return cmp.Compare(a.ISO2, b.ISO2) })
	return out, nil
}

// Country returns the country with the ISO2 code (any case).
func (c *Client) Country(ctx context.Context, iso2 string) (Country, bool, error) {
	all, err := c.Countries(ctx)
	if err != nil {
		return Country{}, false, err
	}
	iso2 = strings.ToUpper(iso2)
	i, ok := slices.BinarySearchFunc(all, iso2, func(c Country, k string) int { return cmp.Compare(c.ISO2, k) })
	if !ok {
		return Country{}, false, nil
	}
	return all[i], true, nil
}

// RegionsOf returns the regions of a country, sorted by name, and whether the country exists. A country may exist with no regions.
func (c *Client) RegionsOf(ctx context.Context, iso2 string) ([]Region, bool, error) {
	country, ok, err := c.Country(ctx, iso2)
	if err != nil || !ok {
		return nil, ok, err
	}
	items, err := c.list(ctx, "/regions?"+url.Values{"country": {country.ISO2}}.Encode())
	if err != nil {
		return nil, false, err
	}
	out := make([]Region, 0, len(items))
	for _, p := range items {
		out = append(out, Region{ID: country.ISO2 + "." + p.Code, Code: p.Code, Name: p.Name, ASCIIName: p.ASCIIName, GeonameID: p.GeonameID})
	}
	slices.SortFunc(out, func(a, b Region) int { return compareNames(a.ASCIIName, a.Name, a.ID, b.ASCIIName, b.Name, b.ID) })
	return out, true, nil
}

// DistrictsOf returns the districts of a region ("TR.34", any case), sorted by name, and whether the region exists. A region may exist with no districts.
func (c *Client) DistrictsOf(ctx context.Context, regionID string) ([]District, bool, error) {
	country, _, found := strings.Cut(regionID, ".")
	if !found {
		return nil, false, nil
	}
	regions, ok, err := c.RegionsOf(ctx, country)
	if err != nil || !ok {
		return nil, false, err
	}
	i := slices.IndexFunc(regions, func(r Region) bool { return strings.EqualFold(r.ID, regionID) })
	if i < 0 {
		return nil, false, nil
	}
	r := regions[i]
	items, err := c.list(ctx, "/districts?"+url.Values{"country": {strings.ToUpper(country)}, "region": {r.Code}}.Encode())
	if err != nil {
		return nil, false, err
	}
	out := make([]District, 0, len(items))
	for _, p := range items {
		id := p.Code // a town: its GeoNames id
		if p.Kind != KindCity {
			id = r.ID + "." + p.Code
		}
		out = append(out, District{ID: id, Name: p.Name, ASCIIName: p.ASCIIName, Kind: cmp.Or(p.Kind, KindAdmin2)})
	}
	slices.SortFunc(out, func(a, b District) int { return compareNames(a.ASCIIName, a.Name, a.ID, b.ASCIIName, b.Name, b.ID) })
	return out, true, nil
}

// list reads one list, from memory while it is younger than TTL. A failed read serves the older copy when there is one.
func (c *Client) list(ctx context.Context, path string) ([]place, error) {
	c.mu.Lock()
	e, ok := c.cache[path]
	c.mu.Unlock()
	if !ok || c.now().Sub(e.fetched) >= TTL {
		body, err := c.fetch(ctx, path)
		switch {
		case err == nil:
			e = entry{body: body, fetched: c.now()}
			c.mu.Lock()
			c.cache[path] = e
			c.mu.Unlock()
		case !ok:
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		default: // nafru-geo is down: the older copy is served and the next read waits a minute (no ten-second wait on every request)
			e.fetched = c.now().Add(RetryAfter - TTL)
			c.mu.Lock()
			c.cache[path] = e
			c.mu.Unlock()
		}
	}
	var v struct {
		Items []place `json:"items"`
	}
	if err := json.Unmarshal(e.body, &v); err != nil {
		return nil, fmt.Errorf("%w: decode %s: %w", ErrUnavailable, path, err)
	}
	return v.Items, nil
}

func (c *Client) fetch(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("nafru-geo answered " + strconv.Itoa(resp.StatusCode))
	}
	var raw json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
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
