import type { Call } from "./gateway";

/**
 * A small stand-in for the gateway's GeoNames lists (GET /app/v1/geo/...): a few countries, the regions of some of them, the districts of a few regions.
 * Monaco is in the list with no regions, so the form's free-text fallback can be seen. Every mocked gateway answers with it unless a test answers first.
 */
export const geoCountries = [
  { iso2: "DE", iso3: "DEU", name: "Germany", continent: "EU", geoname_id: 2921044 },
  { iso2: "FR", iso3: "FRA", name: "France", continent: "EU", geoname_id: 3017382 },
  { iso2: "GB", iso3: "GBR", name: "United Kingdom", continent: "EU", geoname_id: 2635167 },
  { iso2: "MC", iso3: "MCO", name: "Monaco", continent: "EU", geoname_id: 2993457 },
  { iso2: "SE", iso3: "SWE", name: "Sweden", continent: "EU", geoname_id: 2661886 },
  { iso2: "TR", iso3: "TUR", name: "Turkey", continent: "AS", geoname_id: 298795 },
  { iso2: "US", iso3: "USA", name: "United States", continent: "NA", geoname_id: 6252001 },
];

const region = (id: string, name: string, ascii = name) => ({ id, code: id.split(".")[1] ?? "", name, ascii_name: ascii, geoname_id: 1 });
export const geoRegions: Record<string, ReturnType<typeof region>[]> = {
  DE: [region("DE.01", "Baden-Württemberg", "Baden-Wuerttemberg"), region("DE.16", "Berlin")],
  TR: [region("TR.68", "Ankara"), region("TR.34", "Istanbul"), region("TR.35", "İzmir", "Izmir")],
  US: [region("US.CA", "California"), region("US.NY", "New York")],
  SE: [region("SE.26", "Stockholm")],
};

const district = (id: string, name: string, ascii: string, kind: "admin2" | "city" = "admin2") => ({ id, name, ascii_name: ascii, kind });
export const geoDistricts: Record<string, ReturnType<typeof district>[]> = {
  "TR.34": [district("TR.34.7732457", "Bahçelievler", "Bahcelievler"), district("TR.34.7732503", "Beşiktaş", "Besiktas"), district("TR.34.7732501", "Kadıköy", "Kadikoey"), district("745044", "Üsküdar", "Uskudar", "city")],
  "TR.68": [district("TR.68.7732434", "Çankaya", "Cankaya")],
  "US.CA": [district("5368361", "Los Angeles", "Los Angeles", "city"), district("5391959", "San Francisco", "San Francisco", "city")],
  "DE.16": [district("DE.16.00", "Berlin", "Berlin")],
};

const attribution = "Data from GeoNames (https://www.geonames.org), licensed under CC BY 4.0";
const version = "geonames-test";

export function geoHandler(c: Call): { status?: number; body?: unknown } | undefined {
  if (c.method !== "GET" || !c.path.startsWith("/app/v1/geo/")) return undefined;
  if (c.path === "/app/v1/geo/countries") return { body: { version, attribution, countries: geoCountries } };
  let m = /^\/app\/v1\/geo\/countries\/([^/]+)\/regions$/.exec(c.path);
  if (m) {
    const iso2 = decodeURIComponent(m[1] ?? "").toUpperCase();
    if (!geoCountries.some((x) => x.iso2 === iso2)) return { status: 404, body: { error: { code: "NOT_FOUND", key: "gateway.geo.country_not_found", message: "no such country" } } };
    return { body: { version, attribution, country: iso2, regions: geoRegions[iso2] ?? [] } };
  }
  m = /^\/app\/v1\/geo\/regions\/([^/]+)\/districts$/.exec(c.path);
  if (m) {
    const id = decodeURIComponent(m[1] ?? "");
    if (!Object.values(geoRegions).some((rs) => rs.some((r) => r.id === id))) return { status: 404, body: { error: { code: "NOT_FOUND", key: "gateway.geo.region_not_found", message: "no such region" } } };
    return { body: { version, attribution, region: id, districts: geoDistricts[id] ?? [] } };
  }
  return undefined;
}
