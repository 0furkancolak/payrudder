# Countries, regions and districts (GeoNames)

The console's address forms (customer address, seller address, the live billing profile) and its country fields (tax rates, payment provider routing)
offer the countries of the world, the first-level divisions of each country (states, provinces, counties: "regions") and, under each region, its
second-level divisions plus the towns of 15000 people or more ("districts"). The lists come from [GeoNames](https://www.geonames.org).

## Licence and attribution

GeoNames data is licensed under the [Creative Commons Attribution 4.0 License](https://creativecommons.org/licenses/by/4.0/) (CC BY 4.0). The credit is
given where the data is used:

- every answer of the gateway's `/app/v1/geo/*` routes carries `attribution`: "Data from GeoNames (https://www.geonames.org), licensed under CC BY 4.0";
- [NOTICE](../NOTICE) names the source. The console shows no credit line next to the fields (phase 50 took it out: the licence asks for attribution
  where the data is distributed, which the API answer and NOTICE do).

The data was changed: the importer keeps only some columns (codes, names, ASCII names, GeoNames ids), leaves out the dissolved countries GeoNames still
lists (CS Serbia and Montenegro, AN Netherlands Antilles), and merges towns into the second-level divisions of their region (a town with the name of a
division is listed once). Country names are shown in the page's language by the browser (`Intl.DisplayNames`); GeoNames' English name is the fallback.
Names are described in the next section.

## Names

GeoNames names a division with what it is: "Adıyaman Province", "Kadıköy İlçesi", "Cook County", "State of Alabama". An address does not carry that word, so
the importer makes the name an address would use:

1. **Local language first.** The importer reads `alternateNamesV2` (205 MB zipped, streamed: only the ids of the regions, districts and towns and only the
   languages needed are kept) and takes, for a place, the alternate name in the first language of its country's `countryInfo` row ("tr" for TR) when it
   is not short, colloquial or historic and is written in Latin script. Otherwise GeoNames' own name is kept when it is Latin script ("Kadıköy İlçesi" is
   already Turkish), and only when that is not (Cyrillic, Arabic, Han) the English alternate name is used. A name never changes script: the ASCII name
   stays the search key and the sort key.
2. **Generic words go** (`CleanName` in `internal/geo/names.go`): trailing "Province", "Region", "County", "Department", "District", "Oblast", "State",
   "Governorate", "Municipality", "Parish", "Prefecture", "İlçesi"/"ilçesi", "Kommun", "pagasts" and the like, any number of them ("Foo District
   Municipality" is "Foo"); leading "State of", "Région", "Land", "Département de", "Comuna", "Amphoe", "Municipio" and the like; Japanese "-ken", "-shi" and
   "-gun". A name that is nothing but such a word is kept, and islands, atolls and cities ("Rhode Island", "Quezon City") are left alone because they are the name.
3. **ASCII fallback.** `ascii_name` is GeoNames' ASCII name cleaned the same way; the console searches the name, the ASCII name and the code, so "istanbul" finds
   "İstanbul" and "kadikoy" finds "Kadıköy". A stored address (which keeps the name as text) is still matched by name or ASCII name, case and accents ignored.

`TestEmbeddedNamesHaveNoSuffix` fails when a served name (or ASCII name) would be changed by `CleanName` again; a change of the lists in `names.go` needs a
re-import, which `make geo-data` does. `-no-alternates` skips the big download (names then come from GeoNames' own columns, cleaned).

## Where it lives

| Piece | Path |
|---|---|
| Importer | `services/gateway/cmd/geoimport` |
| Builder, format, lookups | `services/gateway/internal/geo` |
| The generated file (embedded, about 1.1 MB) | `services/gateway/internal/geo/data/geonames.json.gz` |
| Routes | `services/gateway/internal/httpapi/geo.go`, documented in `api/openapi/v1.yaml` |
| Console | `web/admin/src/shared/ui/location-fields` (`LocationFields`, `CountrySelect`), `web/admin/src/shared/lib/geo.ts` |
| Mock for the console tests | `web/admin/e2e-mock/geo.ts` |

The generated file is committed and embedded in the gateway binary, so a build and the tests need no network. A test fails if it grows past 5 MiB.

## Regenerating

```sh
make geo-data                         # downloads the dumps from download.geonames.org and rewrites the file
# or, offline, from dumps already on disk:
cd services/gateway && go run ./cmd/geoimport -src /path/to/dumps -version geonames-2026-10-26
```

The importer reads `countryInfo.txt`, `admin1CodesASCII.txt`, `admin2Codes.txt`, `cities15000.zip` (`-no-cities` leaves the towns out) and `alternateNamesV2.zip`
(`-no-alternates` leaves the local names out). The same
dumps and `-version` give the same file byte for byte (sorted lists, no time stamp in the gzip header), so a refresh shows up as a reviewable change of
one file. Commit it with the version in the message; nothing else needs to change.

## API

All three are `GET`, need a session (`Authorization: Bearer qs_…`; an operator's session is one), are rate limited as reads per person, and answer the
same to everybody: nothing about the caller is in the answer. They are cached by the browser for a day (`Cache-Control: private, max-age=86400`) and
revalidated by `ETag` (`304`).

| Route | Answer |
|---|---|
| `/app/v1/geo/countries` | every country: `iso2`, `iso3`, English `name`, `continent`, `geoname_id` |
| `/app/v1/geo/countries/{iso2}/regions` | the country's regions, by name: `id` (`TR.34`), `code` (`34`), `name`, `ascii_name`; `404 gateway.geo.country_not_found` |
| `/app/v1/geo/regions/{id}/districts` | the region's districts, by name: `id`, `name`, `ascii_name`, `kind` (`admin2` or `city`); `404 gateway.geo.region_not_found` |

## The stored address does not change

An address is still the country code (ISO 3166-1 alpha-2) and the region and city as free text (`Address` in the API). The lists only help to fill
them: the region field stores the region's name, the city field the district's name. Any text is still accepted (the fields offer "Use “…”" for a
place the list lacks, and are plain text inputs when a country has no regions in the list or a region has no districts), and an address saved before
the lists existed loads and saves unchanged. A stored region is recognised again by its name or ASCII name (case and accents ignored), its code or its
id, so its districts are offered.
