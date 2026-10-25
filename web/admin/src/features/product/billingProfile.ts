import { cleanAddress, validateAddress, type Address, type AddressProblem } from "@/shared/lib/address";
import type { components } from "@/shared/api/schema";

type Tenant = components["schemas"]["Tenant"];

/**
 * The countries whose invoices need the seller's tax id: Turkey and the EU member states. The same list as `taxIDCountries` in
 * services/plans-svc/internal/domain/golive.go (a test reads that file and compares), because the server decides and the form only saves a round trip.
 */
export const TAX_ID_COUNTRIES = [
  "TR", "AT", "BE", "BG", "HR", "CY", "CZ", "DK", "EE", "FI", "FR", "DE", "GR", "HU", "IE", "IT", "LV", "LT", "LU", "MT", "NL", "PL", "PT", "RO", "SK", "SI", "ES", "SE",
] as const;

export const taxIdRequired = (country: string): boolean => (TAX_ID_COUNTRIES as readonly string[]).includes(country.trim().toUpperCase());

/** A tax id as it is stored: no spaces, dashes or dots, in capitals ("de 123-456-789" is the VAT id DE123456789). The server does the same. */
export const normalizeTaxId = (id: string): string => id.replace(/[\s.-]/g, "").toUpperCase();

/** What the owner types in the billing profile form. */
export interface ProfileForm {
  legalName: string;
  taxId: string;
  taxOffice: string;
  country: string;
  line1: string;
  line2: string;
  city: string;
  region: string;
  postalCode: string;
}

export const emptyProfile = (): ProfileForm => ({ legalName: "", taxId: "", taxOffice: "", country: "", line1: "", line2: "", city: "", region: "", postalCode: "" });

export type ProfileField = keyof ProfileForm;
/** Why a field is refused: the form shows `product.goLive.profile.problems.<code>`. */
export type ProfileProblem = "required" | "taxIdFormat" | AddressProblem;
export type ProfileProblems = Partial<Record<ProfileField, ProfileProblem>>;

const text = (s: string) => s.trim();

/** The form filled from the stored live tenant (an empty form for a tenant with nothing saved). */
export function fromTenant(t: Tenant | undefined): ProfileForm {
  return {
    legalName: t?.legal?.legal_name ?? "",
    taxId: t?.legal?.tax_id ?? "",
    taxOffice: t?.legal?.tax_office ?? "",
    country: (t?.address?.country ?? "").toUpperCase(),
    line1: t?.address?.line1 ?? "",
    line2: t?.address?.line2 ?? "",
    city: t?.address?.city ?? "",
    region: t?.address?.region ?? "",
    postalCode: t?.address?.postal_code ?? "",
  };
}

/** The address part of the form, as the address helpers take it. */
export function addressOf(f: ProfileForm): Address {
  return cleanAddress({ line1: f.line1, line2: f.line2, city: f.city, region: f.region, postal_code: f.postalCode, country: f.country });
}

/**
 * The same rules as the server (`domain.ValidateBillingProfile`): a legal name, a country, the first address line and the city, and a tax id where the
 * country's invoices need one (5 to 20 letters and digits). An empty result means the form can be sent.
 */
export function validateProfile(f: ProfileForm): ProfileProblems {
  const out: ProfileProblems = {};
  if (text(f.legalName) === "") out.legalName = "required";
  else if ([...text(f.legalName)].length > 200) out.legalName = "tooLong";
  const address = addressOf(f);
  if (!address.country) out.country = "required";
  if (!address.line1) out.line1 = "required";
  if (!address.city) out.city = "required";
  const parts = validateAddress(address);
  const map: [keyof Address, ProfileField][] = [["line1", "line1"], ["line2", "line2"], ["city", "city"], ["region", "region"], ["postal_code", "postalCode"], ["country", "country"]];
  for (const [part, field] of map) {
    const problem = parts[part];
    if (problem && !out[field]) out[field] = problem;
  }
  const taxId = normalizeTaxId(f.taxId);
  if (taxId === "") {
    if (taxIdRequired(f.country)) out.taxId = "required";
  } else if (!/^[A-Z0-9]{5,20}$/.test(taxId)) out.taxId = "taxIdFormat";
  return out;
}

/** The request body for `PUT /app/v1/apps/{id}/billing-profile`. */
export function profileBody(f: ProfileForm): { legal: components["schemas"]["LegalInfo"]; address: Address } {
  const legal: components["schemas"]["LegalInfo"] = { legal_name: text(f.legalName) };
  if (normalizeTaxId(f.taxId) !== "") legal.tax_id = normalizeTaxId(f.taxId);
  if (text(f.taxOffice) !== "") legal.tax_office = text(f.taxOffice);
  return { legal, address: addressOf(f) };
}
