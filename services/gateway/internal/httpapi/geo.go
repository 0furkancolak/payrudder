package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/gateway/internal/geo"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// geoRoutes is the table of the country, region and district lists the console's address forms offer (GeoNames, embedded in the gateway). They are the
// same for everybody: a signed-in person (an operator is one too) reads them, and nothing about the caller is in the answer.
func (s *Server) geoRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/geo/countries", AccessSession, ratelimit.ClassRead, s.geoCountries},
		{"GET", "/app/v1/geo/countries/{iso2}/regions", AccessSession, ratelimit.ClassRead, s.geoRegions},
		{"GET", "/app/v1/geo/regions/{id}/districts", AccessSession, ratelimit.ClassRead, s.geoDistricts},
	}
}

// geoCacheControl lets the browser keep a list for a day: it changes only when the gateway ships a new export. "private" keeps it out of shared caches,
// which see an Authorization header on the request.
const geoCacheControl = "private, max-age=86400"

var (
	iso2Re     = regexp.MustCompile(`^[A-Za-z]{2}$`)
	regionIDRe = regexp.MustCompile(`^[A-Za-z]{2}\.[A-Za-z0-9]{1,20}$`)
)

type geoCountriesResponse struct {
	Version     string        `json:"version"`
	Attribution string        `json:"attribution"`
	Countries   []geo.Country `json:"countries"`
}

type geoRegionsResponse struct {
	Version     string       `json:"version"`
	Attribution string       `json:"attribution"`
	Country     string       `json:"country"`
	Regions     []geo.Region `json:"regions"`
}

type geoDistrictsResponse struct {
	Version     string         `json:"version"`
	Attribution string         `json:"attribution"`
	Region      string         `json:"region"`
	Districts   []geo.District `json:"districts"`
}

func geoData() (*geo.Dataset, error) {
	d, err := geo.Default()
	if err != nil {
		return nil, status.Error(codes.Internal, "geo data unavailable")
	}
	return d, nil
}

func (s *Server) geoCountries(w http.ResponseWriter, r *http.Request) {
	d, err := geoData()
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeGeo(w, r, geoCountriesResponse{Version: d.Version, Attribution: geo.Attribution, Countries: d.Countries})
}

func (s *Server) geoRegions(w http.ResponseWriter, r *http.Request) {
	iso2 := r.PathValue("iso2")
	if !iso2Re.MatchString(iso2) {
		writeError(w, r, invalid("iso2", "an ISO 3166-1 alpha-2 country code"))
		return
	}
	d, err := geoData()
	if err != nil {
		writeError(w, r, err)
		return
	}
	regions, ok := d.RegionsOf(iso2)
	if !ok {
		writeError(w, r, errkey.New(codes.NotFound, "gateway.geo.country_not_found", "no such country"))
		return
	}
	if regions == nil {
		regions = []geo.Region{}
	}
	writeGeo(w, r, geoRegionsResponse{Version: d.Version, Attribution: geo.Attribution, Country: strings.ToUpper(iso2), Regions: regions})
}

func (s *Server) geoDistricts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !regionIDRe.MatchString(id) {
		writeError(w, r, invalid("id", "a region id such as TR.34"))
		return
	}
	d, err := geoData()
	if err != nil {
		writeError(w, r, err)
		return
	}
	districts, ok := d.DistrictsOf(id)
	if !ok {
		writeError(w, r, errkey.New(codes.NotFound, "gateway.geo.region_not_found", "no such region"))
		return
	}
	if districts == nil {
		districts = []geo.District{}
	}
	country, code, _ := strings.Cut(id, ".")
	writeGeo(w, r, geoDistrictsResponse{Version: d.Version, Attribution: geo.Attribution, Region: strings.ToUpper(country) + "." + code, Districts: districts})
}

// writeGeo writes a list with a day's cache and an ETag, answering 304 when the browser already has it.
func writeGeo(w http.ResponseWriter, r *http.Request, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeError(w, r, status.Error(codes.Internal, "encode response"))
		return
	}
	sum := sha256.Sum256(b)
	tag := `"` + hex.EncodeToString(sum[:12]) + `"`
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", geoCacheControl)
	h.Set("ETag", tag)
	if etagMatches(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
