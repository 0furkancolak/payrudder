import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { TAX_ID_COUNTRIES, emptyProfile, fromTenant, normalizeTaxId, profileBody, taxIdRequired, validateProfile, type ProfileForm } from "./billingProfile";

const repo = (path: string) => fileURLToPath(new URL(`../../../../../${path}`, import.meta.url));

const complete = (over: Partial<ProfileForm> = {}): ProfileForm => ({
  ...emptyProfile(), legalName: "Shop GmbH", taxId: "DE123456789", country: "DE", line1: "Hauptstr. 1", city: "Berlin", postalCode: "10115", ...over,
});

describe("which countries need the seller's tax id", () => {
  it("is the list the server uses", () => {
    const source = readFileSync(repo("services/plans-svc/internal/domain/golive.go"), "utf8");
    const block = /var taxIDCountries = map\[string\]bool\{([^}]*)\}/s.exec(source)?.[1] ?? "";
    const server = [...block.matchAll(/"([A-Z]{2})":\s*true/g)].map((m) => m[1] as string).sort();
    expect(server.length).toBeGreaterThan(20);
    expect([...TAX_ID_COUNTRIES].sort()).toEqual(server);
  });

  it("answers in any case and ignores spaces", () => {
    expect(taxIdRequired("tr")).toBe(true);
    expect(taxIdRequired(" de ")).toBe(true);
    expect(taxIdRequired("US")).toBe(false);
    expect(taxIdRequired("")).toBe(false);
  });
});

describe("the tax id", () => {
  it("is stored without spaces, dashes or dots, in capitals", () => {
    expect(normalizeTaxId("de 123-456.789")).toBe("DE123456789");
    expect(normalizeTaxId("")).toBe("");
  });
});

describe("validating the billing profile", () => {
  it("accepts a complete profile", () => {
    expect(validateProfile(complete())).toEqual({});
  });

  it("names every missing part", () => {
    expect(validateProfile(emptyProfile())).toEqual({ legalName: "required", country: "required", line1: "required", city: "required" });
    expect(validateProfile(complete({ legalName: "   " })).legalName).toBe("required");
    expect(validateProfile(complete({ line1: "" })).line1).toBe("required");
    expect(validateProfile(complete({ city: " " })).city).toBe("required");
  });

  it("asks for a tax id only where the country needs one", () => {
    expect(validateProfile(complete({ taxId: "" })).taxId).toBe("required");
    expect(validateProfile(complete({ country: "tr", taxId: "" })).taxId).toBe("required");
    expect(validateProfile(complete({ country: "US", taxId: "" }))).toEqual({});
  });

  it("refuses a tax id that is not 5 to 20 letters and digits, after tidying it", () => {
    expect(validateProfile(complete({ taxId: "DE 1234 5678 9" }))).toEqual({});
    expect(validateProfile(complete({ taxId: "DE12" })).taxId).toBe("taxIdFormat");
    expect(validateProfile(complete({ taxId: "DE!23456789" })).taxId).toBe("taxIdFormat");
    expect(validateProfile(complete({ country: "US", taxId: "12" })).taxId).toBe("taxIdFormat");
  });

  it("applies the address rules of the rest of the console", () => {
    expect(validateProfile(complete({ country: "Germany" })).country).toBe("badCountry");
    expect(validateProfile(complete({ line1: "x".repeat(121) })).line1).toBe("tooLong");
    expect(validateProfile(complete({ city: "a\nb" })).city).toBe("oneLine");
    expect(validateProfile(complete({ legalName: "x".repeat(201) })).legalName).toBe("tooLong");
  });
});

describe("the request body", () => {
  it("is trimmed and normalised, and leaves empty optional parts out", () => {
    expect(profileBody(complete({ legalName: " Shop GmbH ", taxId: "de 123456789", country: "de", line1: " Hauptstr. 1 ", postalCode: "", taxOffice: " " }))).toEqual({
      legal: { legal_name: "Shop GmbH", tax_id: "DE123456789" },
      address: { line1: "Hauptstr. 1", city: "Berlin", country: "DE" },
    });
  });

  it("round-trips through the stored tenant", () => {
    const body = profileBody(complete({ taxOffice: "Kadikoy" }));
    expect(fromTenant(body)).toEqual(complete({ taxOffice: "Kadikoy" }));
    expect(fromTenant(undefined)).toEqual(emptyProfile());
  });
});
