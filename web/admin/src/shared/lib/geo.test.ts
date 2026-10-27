import { describe, expect, it } from "vitest";
import { filterOptions, resolveTyped } from "@/shared/ui/combobox/filter";
import { countryName, countryOptions, districtOptions, findRegion, regionOptions, typedCountry, typedPlace, type GeoCountry, type GeoRegion } from "./geo";

const countries: GeoCountry[] = [
  { iso2: "DE", iso3: "DEU", name: "Germany", continent: "EU" },
  { iso2: "TR", iso3: "TUR", name: "Turkey", continent: "AS" },
  { iso2: "BD", iso3: "BGD", name: "Bangladesh", continent: "AS" },
  { iso2: "XK", iso3: "XKX", name: "Kosovo", continent: "EU" },
];
const regions: GeoRegion[] = [
  { id: "TR.68", code: "68", name: "Ankara", ascii_name: "Ankara" },
  { id: "TR.34", code: "34", name: "Istanbul", ascii_name: "Istanbul" },
  { id: "TR.35", code: "35", name: "İzmir", ascii_name: "Izmir" },
];

describe("countries", () => {
  it("are named in the page's language and sorted by that name", () => {
    const tr = countryOptions(countries, "tr");
    expect(tr.find((o) => o.value === "DE")?.label).toBe("Almanya");
    expect(tr.find((o) => o.value === "TR")?.label).toBe("Türkiye");
    const en = countryOptions(countries, "en").map((o) => o.value);
    expect(en).toEqual(["BD", "DE", "XK", "TR"]);
  });

  it("are found by the English name and the codes in any language", () => {
    const tr = countryOptions(countries, "tr");
    expect(filterOptions(tr, "germany").map((o) => o.value)).toEqual(["DE"]);
    expect(filterOptions(tr, "deu").map((o) => o.value)).toEqual(["DE"]);
    // "de" is in Bangladesh too; the exact code comes first.
    expect(filterOptions(countryOptions(countries, "en"), "de")[0]?.value).toBe("DE");
  });

  it("show the code alone when asked to (narrow rows)", () => {
    expect(countryOptions(countries, "en", true).find((o) => o.value === "TR")?.short).toBe("TR");
    expect(countryOptions(countries, "en").find((o) => o.value === "TR")?.short).toBe(countryName("TR", "en"));
  });

  it("fall back to the English name when the browser has none", () => {
    expect(countryName("AA", "en", "Nowhere")).toBe("Nowhere");
    expect(countryName("AA", "en")).toBe("AA");
  });

  it("put the empty choice after what the search is for", () => {
    const options = [{ value: "", label: "No country", short: "No country" }, ...countryOptions(countries, "en")];
    const typed = (q: string) => typedCountry(q, (c) => `Use the code ${c}`);
    expect(filterOptions(options, "no", typed).map((o) => o.value)).toEqual(["NO", ""]);
    expect(filterOptions(options, "", typed)[0]?.value).toBe("");
  });

  it("take any two-letter code typed, in capitals, and nothing else", () => {
    const label = (c: string) => `use ${c}`;
    expect(typedCountry(" se ", label)).toEqual({ value: "SE", label: "use SE", short: "SE" });
    expect(typedCountry("s1", label)).toBeNull();
    expect(typedCountry("swe", label)).toBeNull();
  });
});

describe("regions", () => {
  it("are found again from the text an address stored", () => {
    expect(findRegion(regions, "istanbul")?.id).toBe("TR.34");
    expect(findRegion(regions, " İzmir ")?.id).toBe("TR.35");
    expect(findRegion(regions, "izmir")?.id).toBe("TR.35");
    expect(findRegion(regions, "34")?.id).toBe("TR.34");
    expect(findRegion(regions, "tr.68")?.id).toBe("TR.68");
    expect(findRegion(regions, "Marmara")).toBeUndefined();
    expect(findRegion(regions, "")).toBeUndefined();
  });

  it("keep their name as the value, so the stored address stays text", () => {
    expect(regionOptions(regions)[1]).toEqual({ value: "Istanbul", label: "Istanbul", keywords: "Istanbul 34" });
    expect(districtOptions([{ id: "TR.34.1", name: "Kadıköy", ascii_name: "Kadikoey", kind: "admin2" }])[0]).toEqual({ value: "Kadıköy", label: "Kadıköy", keywords: "Kadikoey" });
  });
});

describe("free text next to a list", () => {
  const places = districtOptions([
    { id: "1", name: "Kadıköy", ascii_name: "Kadikoey", kind: "admin2" },
    { id: "2", name: "Beşiktaş", ascii_name: "Besiktas", kind: "admin2" },
  ]);
  const typed = (q: string) => typedPlace(q, (v) => `Use “${v}”`);

  it("offers what was typed after the matches, unless a place already is it", () => {
    expect(filterOptions(places, "kadikoy", typed).map((o) => o.value)).toEqual(["Kadıköy"]);
    expect(filterOptions(places, "kadi", typed).map((o) => o.value)).toEqual(["Kadıköy", "kadi"]);
    expect(filterOptions(places, "Kadıköy", typed).map((o) => o.value)).toEqual(["Kadıköy"]);
    expect(filterOptions(places, "Moda", typed).map((o) => o.label)).toEqual(["Use “Moda”"]);
    expect(filterOptions(places, "Moda").length).toBe(0);
    expect(filterOptions(places, "", typed).length).toBe(2);
  });

  it("leaving the field picks the place the text names, else keeps the text", () => {
    expect(resolveTyped(places, "besiktas", typed)).toBe("Beşiktaş");
    expect(resolveTyped(places, "  Moda ", typed)).toBe("Moda");
    expect(resolveTyped(places, "   ", typed)).toBe("");
    // A picker without free text only takes a place it has.
    expect(resolveTyped(places, "Moda")).toBeUndefined();
  });

  it("is refused past the length the API takes", () => {
    expect(typedPlace("x".repeat(121), (v) => v)).toBeNull();
    expect(typedPlace("x".repeat(120), (v) => v)?.value).toHaveLength(120);
  });
});
