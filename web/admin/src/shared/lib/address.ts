/** A postal address as the API takes it (`Address` in api/openapi/v1.yaml). Every part is optional. */
export interface Address {
  line1?: string;
  line2?: string;
  city?: string;
  region?: string;
  postal_code?: string;
  country?: string;
}

export type AddressPart = keyof Address;

/** The longest each part may be, as plans-svc enforces it (`domain.ValidateAddress`): 120 characters, the postal code 20. */
export const ADDRESS_MAX: Readonly<Record<AddressPart, number>> = { line1: 120, line2: 120, city: 120, region: 120, postal_code: 20, country: 2 };

/** What is wrong with one part, as the key under `address.problems` in the locale files. */
export type AddressProblem = "tooLong" | "oneLine" | "badCountry";

/**
 * The same rules as the backend, so the form refuses what the API would refuse: each part at most {@link ADDRESS_MAX} characters (counted in code
 * points, like Go's RuneCountInString), no line breaks, tabs or NUL, and a country that is two capital letters (ISO 3166-1 alpha-2) or empty.
 * Returns the problem per part; an empty object means valid.
 */
export function validateAddress(a: Address): Partial<Record<AddressPart, AddressProblem>> {
  const out: Partial<Record<AddressPart, AddressProblem>> = {};
  for (const part of ["line1", "line2", "city", "region", "postal_code"] as const) {
    const value = a[part] ?? "";
    if ([...value].length > ADDRESS_MAX[part]) out[part] = "tooLong";
    else if (/[\n\r\t\0]/.test(value)) out[part] = "oneLine";
  }
  const country = a.country ?? "";
  if (country !== "" && !/^[A-Z]{2}$/.test(country)) out.country = "badCountry";
  return out;
}

/** The address as the API should receive it: parts trimmed, empty parts left out, the country in capitals. */
export function cleanAddress(a: Address): Address {
  const out: Address = {};
  for (const part of Object.keys(ADDRESS_MAX) as AddressPart[]) {
    const v = (a[part] ?? "").trim();
    if (v !== "") out[part] = part === "country" ? v.toUpperCase() : v;
  }
  return out;
}

/**
 * The lines printed on an invoice, the same as `address.Lines` in pkg/address: the street lines, then "postal code city", the region and the country
 * code. An empty part leaves no line.
 */
export function addressLines(a: Address): string[] {
  return [a.line1, a.line2, `${a.postal_code ?? ""} ${a.city ?? ""}`, a.region, a.country].map((s) => (s ?? "").trim()).filter((s) => s !== "");
}

/** How a country names the parts of its addresses, so the form asks for the right things. Keys are labels under `address.labels`. */
export interface CountryProfile {
  region: "region" | "state" | "province" | "county" | "prefecture";
  postalCode: "postalCode" | "zip" | "postcode";
  /** Whether addresses there usually have a region; if not the field is tucked away (it stays optional and is still sent when filled). */
  usesRegion: boolean;
  /** What a postal code looks like there. Only a hint: the API accepts any text up to 20 characters. */
  postalPattern?: RegExp;
  example?: string;
}

const DEFAULT_PROFILE: CountryProfile = { region: "region", postalCode: "postalCode", usesRegion: true };

const PROFILES: Record<string, CountryProfile> = {
  TR: { region: "province", postalCode: "postalCode", usesRegion: false, postalPattern: /^\d{5}$/, example: "34000" },
  DE: { region: "state", postalCode: "postalCode", usesRegion: false, postalPattern: /^\d{5}$/, example: "10115" },
  FR: { region: "region", postalCode: "postalCode", usesRegion: false, postalPattern: /^\d{5}$/, example: "75001" },
  ES: { region: "province", postalCode: "postalCode", usesRegion: true, postalPattern: /^\d{5}$/, example: "28001" },
  NL: { region: "province", postalCode: "postalCode", usesRegion: false, postalPattern: /^\d{4} ?[A-Z]{2}$/, example: "1011 AB" },
  IT: { region: "province", postalCode: "postalCode", usesRegion: true, postalPattern: /^\d{5}$/, example: "00100" },
  US: { region: "state", postalCode: "zip", usesRegion: true, postalPattern: /^\d{5}(-\d{4})?$/, example: "94105" },
  CA: { region: "province", postalCode: "postalCode", usesRegion: true, postalPattern: /^[A-Z]\d[A-Z] ?\d[A-Z]\d$/, example: "K1A 0B1" },
  GB: { region: "county", postalCode: "postcode", usesRegion: false, postalPattern: /^[A-Z]{1,2}\d[A-Z\d]? ?\d[A-Z]{2}$/, example: "SW1A 1AA" },
  CH: { region: "region", postalCode: "postalCode", usesRegion: false, postalPattern: /^\d{4}$/, example: "8001" },
  JP: { region: "prefecture", postalCode: "postalCode", usesRegion: true, postalPattern: /^\d{3}-?\d{4}$/, example: "100-0001" },
  KW: { region: "region", postalCode: "postalCode", usesRegion: false },
};

export function countryProfile(country: string | undefined): CountryProfile {
  return PROFILES[(country ?? "").toUpperCase()] ?? DEFAULT_PROFILE;
}

/** A postal code that does not look like the country's (a hint, never a refusal). Empty codes and countries without a pattern are fine. */
export function postalCodeLooksOff(country: string | undefined, postalCode: string | undefined): boolean {
  const pattern = countryProfile(country).postalPattern;
  const code = (postalCode ?? "").trim().toUpperCase();
  return pattern !== undefined && code !== "" && !pattern.test(code);
}
