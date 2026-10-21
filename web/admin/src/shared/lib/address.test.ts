import { describe, expect, it } from "vitest";
import { addressLines, cleanAddress, countryProfile, postalCodeLooksOff, validateAddress } from "./address";

// These mirror pkg/address (TestLines) and plans-svc domain.TestValidateAddress: the console must say what the backend says.
describe("addressLines", () => {
  it("prints the street lines, then postal code and city, region and country", () => {
    expect(addressLines({ line1: "Atatürk Cd. 1", line2: "Kat 2", city: "İstanbul", region: "Marmara", postal_code: "34000", country: "TR" })).toEqual([
      "Atatürk Cd. 1",
      "Kat 2",
      "34000 İstanbul",
      "Marmara",
      "TR",
    ]);
  });
  it("leaves no line for an empty part", () => {
    expect(addressLines({ city: "Berlin", country: "DE" })).toEqual(["Berlin", "DE"]);
    expect(addressLines({ postal_code: "10115" })).toEqual(["10115"]);
    expect(addressLines({})).toEqual([]);
    expect(addressLines({ line1: " ", city: "  " })).toEqual([]);
  });
});

describe("validateAddress", () => {
  it("accepts a good address, an empty one and a long non-ASCII one", () => {
    expect(validateAddress({ line1: "Atatürk Cd. 1", city: "İstanbul", postal_code: "34000", country: "TR" })).toEqual({});
    expect(validateAddress({})).toEqual({});
    expect(validateAddress({ line1: "ı".repeat(120) })).toEqual({});
  });
  it("refuses what plans-svc refuses", () => {
    expect(
      validateAddress({ line1: "x".repeat(121), line2: "two\nlines", postal_code: "9".repeat(21), country: "turkey", city: "a\tb", region: "r".repeat(120) }),
    ).toEqual({ line1: "tooLong", line2: "oneLine", postal_code: "tooLong", country: "badCountry", city: "oneLine" });
  });
  it("counts characters like the backend (code points), not UTF-16 units", () => {
    expect(validateAddress({ line1: "😀".repeat(120) })).toEqual({});
    expect(validateAddress({ line1: "😀".repeat(121) })).toEqual({ line1: "tooLong" });
  });
  it("wants the country in capitals", () => {
    expect(validateAddress({ country: "tr" })).toEqual({ country: "badCountry" });
    expect(validateAddress({ country: "T" })).toEqual({ country: "badCountry" });
    expect(validateAddress({ country: "TR" })).toEqual({});
  });
});

describe("cleanAddress", () => {
  it("trims, drops empty parts and capitalises the country", () => {
    expect(cleanAddress({ line1: "  Main St 1 ", line2: "", city: " ", country: " se " })).toEqual({ line1: "Main St 1", country: "SE" });
  });
});

describe("country profiles", () => {
  it("names the parts the way the country does", () => {
    expect(countryProfile("US")).toMatchObject({ region: "state", postalCode: "zip" });
    expect(countryProfile("gb")).toMatchObject({ region: "county", postalCode: "postcode", usesRegion: false });
    expect(countryProfile("ZZ")).toMatchObject({ region: "region", postalCode: "postalCode", usesRegion: true });
    expect(countryProfile(undefined).usesRegion).toBe(true);
  });
  it("only hints when a postal code looks off", () => {
    expect(postalCodeLooksOff("TR", "34000")).toBe(false);
    expect(postalCodeLooksOff("TR", "ABC")).toBe(true);
    expect(postalCodeLooksOff("US", "94105-1234")).toBe(false);
    expect(postalCodeLooksOff("GB", "sw1a 1aa")).toBe(false);
    expect(postalCodeLooksOff("KW", "anything")).toBe(false);
    expect(postalCodeLooksOff("TR", "")).toBe(false);
  });
});
