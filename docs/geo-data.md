# Countries, regions and districts (GeoNames)

The console's address forms (customer address, seller address, the live billing profile) and its country fields (tax rates, payment provider routing)
offer the countries of the world, the first-level divisions of each country (states, provinces, counties: "regions") and, under each region, its
second-level divisions or, where a region has none, its larger towns ("districts"). The lists come from [GeoNames](https://www.geonames.org), through
nafru-geo.

## Licence and attribution

GeoNames data is licensed under the [Creative Commons Attribution 4.0 License](https://creativecommons.org/licenses/by/4.0/) (CC BY 4.0). The credit is
given where the data is used:

- every answer of the gateway's `/app/v1/geo/*` routes carries `attribution`: "Data from GeoNames (https://www.geonames.org), licensed under CC BY 4.0";
- [NOTICE](../NOTICE) names the source. The console shows no credit line next to the fields (phase 50 took it out: the licence asks for attribution
  where the data is distributed, which the API answer and NOTICE do).

## Where the lists come from

The gateway reads them from **nafru-geo** (`https://geo.nafru.com`), the GeoNames service the nafru apps share (its own repository,
`nafrucom/nafru-geo`: a Cloudflare Worker over D1). nafru-geo asks for a key per app (`x-api-key`); the gateway's is `PAYRUDDER_GEO_API_KEY`, a secret (one per
environment: staging and production each have their own, see [plan 23](plans/23-staging-and-production.md#geo-nafru-geo)), and the
address is `PAYRUDDER_GEO_URL` (default `https://geo.nafru.com`). The gateway keeps each list in memory for a day (`geo.TTL`). When nafru-geo cannot be
read, the copy it already has is served and the next try waits a minute (`geo.RetryAfter`), so a request never waits on a service that is down; a list
the gateway has never read answers **503 `gateway.geo.unavailable`**, and so does every geo route while the key is not set. The console's address
fields then stay plain text inputs: nothing that stores an address depends on the lists.

nafru-geo keeps only some columns (codes, names, ASCII names, GeoNames ids), drops GeoNames' type words from division names ("Adıyaman Province" is
"Adıyaman"), and lists, under a region, its second-level divisions (`kind: admin2`) or, for a region that has none, its larger towns (`kind: city`, 5000
people or more, at most 300). The gateway maps its answer to the shapes below: a region's `id` is the country and the code (`TR.34`), a division's is
the region's id and its admin2 code (`TR.34.1234`), a town's is its GeoNames id; regions and districts are sorted by ASCII name. Country names are shown
in the page's language by the browser (`Intl.DisplayNames`); GeoNames' English name is the fallback.

Until this change the lists were embedded in the gateway (made by `cmd/geoimport`, with local-language names from `alternateNamesV2` and the towns of
15000 people or more under every region). That importer is gone; the names nafru-geo serves are GeoNames' own (the seat's local spelling for a region
where GeoNames has one), and a region with second-level divisions no longer lists its towns as well.

## Where it lives

| Piece | Path |
|---|---|
| Client (read, cache, map) | `services/gateway/internal/geo` |
| Routes | `services/gateway/internal/httpapi/geo.go`, documented in `api/openapi/v1.yaml` |
| Console | `web/admin/src/shared/ui/location-fields` (`LocationFields`, `CountrySelect`), `web/admin/src/shared/lib/geo.ts` |
| Mock for the console tests | `web/admin/e2e-mock/geo.ts` |
| The service and its importer | `nafrucom/nafru-geo` (`src/index.ts`, `scripts/geo-import.ts`) |

The gateway's tests use a fake nafru-geo (`httptest`), so they need no network and no key.

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
