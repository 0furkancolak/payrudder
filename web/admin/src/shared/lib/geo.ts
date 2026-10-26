import { fold } from "./intl";

/**
 * The country, region and district lists of the address forms (GeoNames, served by the gateway under /app/v1/geo). An address still stores the country
 * code and the region and city as text: these helpers only turn the lists into picker options and find the stored text in them again.
 */

export interface GeoCountry {
  iso2: string;
  iso3: string;
  /** English; the form shows the name in the page's language. */
  name: string;
  continent: string;
}

export interface GeoRegion {
  /** GeoNames admin1 code, "TR.34". */
  id: string;
  code: string;
  name: string;
  ascii_name: string;
}

export interface GeoDistrict {
  id: string;
  name: string;
  ascii_name: string;
  kind: "admin2" | "city";
}

/** A picker row (the shape of SearchSelect's options). */
export interface GeoOption {
  value: string;
  label: string;
  short?: string;
  hint?: string;
  keywords?: string;
}

const namesCache = new Map<string, Intl.DisplayNames | null>();

/** The name of a country in a language ("Almanya" for DE in Turkish); the English name, then the code, when the browser does not know it. */
export function countryName(iso2: string, locale: string, english = ""): string {
  let names = namesCache.get(locale);
  if (names === undefined) {
    try {
      names = new Intl.DisplayNames([locale], { type: "region", fallback: "none" });
    } catch {
      names = null;
    }
    namesCache.set(locale, names);
  }
  let name: string | undefined;
  try {
    name = names?.of(iso2);
  } catch {
    name = undefined;
  }
  return name && name !== iso2 ? name : english || iso2;
}

/**
 * The countries as options, sorted by their name in the language: "Germany" in English and "Almanya" in Turkish, the code at the end of the row, and the
 * English name and the three-letter code searchable too. `codeOnly` makes the closed field show just the code (for narrow rows).
 */
export function countryOptions(countries: readonly GeoCountry[], locale: string, codeOnly = false): GeoOption[] {
  return countries
    .map((c) => {
      const name = countryName(c.iso2, locale, c.name);
      return { value: c.iso2, label: name, short: codeOnly ? c.iso2 : name, hint: c.iso2, keywords: `${c.name} ${c.iso3}` };
    })
    .sort((a, b) => a.label.localeCompare(b.label, locale));
}

export function regionOptions(regions: readonly GeoRegion[]): GeoOption[] {
  return regions.map((r) => ({ value: r.name, label: r.name, keywords: `${r.ascii_name} ${r.code}` }));
}

export function districtOptions(districts: readonly GeoDistrict[]): GeoOption[] {
  return districts.map((d) => ({ value: d.name, label: d.name, keywords: d.ascii_name }));
}

/** The region a stored text names: by its name or ASCII name (case and accents ignored), its code ("34") or its id ("TR.34"). */
export function findRegion(regions: readonly GeoRegion[], text: string | undefined): GeoRegion | undefined {
  const want = fold((text ?? "").trim());
  if (want === "") return undefined;
  return regions.find((r) => fold(r.name) === want || fold(r.ascii_name) === want) ?? regions.find((r) => fold(r.code) === want || fold(r.id) === want);
}

/** A country code the person typed that the list may not have (any two letters, as the API accepts): offered as itself. */
export function typedCountry(query: string, label: (code: string) => string): GeoOption | null {
  const code = query.trim().toUpperCase();
  return /^[A-Z]{2}$/.test(code) ? { value: code, label: label(code), short: code } : null;
}

/** Free text for a region or a city the list does not have. */
export function typedPlace(query: string, label: (text: string) => string, max = 120): GeoOption | null {
  const text = query.trim();
  return text !== "" && [...text].length <= max ? { value: text, label: label(text) } : null;
}
