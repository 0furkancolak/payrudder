import { describe, expect, it } from "vitest";
import { CURRENCIES } from "./currencies";
import { currencyInfo, currencyList, fold, formatOffset, zoneIds, zoneList, zoneMinutes } from "./intl";

describe("currencies", () => {
  it("lists every currency the backend accepts, named in the language, sorted by name", () => {
    const list = currencyList("en");
    expect(list).toHaveLength(CURRENCIES.length);
    expect(list.find((c) => c.code === "JPY")).toMatchObject({ name: "Japanese Yen", digits: 0 });
    expect(list.find((c) => c.code === "KWD")).toMatchObject({ name: "Kuwaiti Dinar", digits: 3 });
    expect(list.find((c) => c.code === "USD")).toMatchObject({ symbol: "$", digits: 2 });
    const names = list.map((c) => c.name);
    expect(names).toEqual([...names].sort((a, b) => a.localeCompare(b, "en")));
  });
  it("uses the fraction digits the currency really has", () => {
    for (const [code, digits] of [["JPY", 0], ["KRW", 0], ["VND", 0], ["BHD", 3], ["OMR", 3], ["TND", 3], ["EUR", 2], ["TRY", 2]] as const) {
      expect(currencyInfo(code, "en").digits, code).toBe(digits);
    }
  });
  it("names in another language", () => {
    expect(currencyInfo("EUR", "tr").name).toBe("Euro");
    expect(currencyInfo("JPY", "de").name).toBe("Japanischer Yen");
  });
});

describe("time zones", () => {
  it("offers a long list with UTC in it", () => {
    const ids = zoneIds();
    expect(ids.length).toBeGreaterThan(300);
    expect(ids).toContain("UTC");
    expect(ids).toContain("Europe/Istanbul");
  });
  it("shows the offset of a zone right now", () => {
    const winter = new Date("2026-01-15T12:00:00Z");
    const summer = new Date("2026-07-15T12:00:00Z");
    expect(zoneMinutes("Europe/Istanbul", winter)).toBe(180);
    expect(zoneMinutes("Europe/Berlin", winter)).toBe(60);
    expect(zoneMinutes("Europe/Berlin", summer)).toBe(120);
    expect(zoneMinutes("Asia/Kolkata", winter)).toBe(330);
    expect(zoneMinutes("UTC", winter)).toBe(0);
    expect(zoneMinutes("America/New_York", winter)).toBe(-300);
    expect(zoneMinutes("Not/AZone", winter)).toBe(0);
    expect(formatOffset(330)).toBe("UTC+05:30");
    expect(formatOffset(-570)).toBe("UTC-09:30");
    expect(formatOffset(0)).toBe("UTC+00:00");
  });
  it("lists every zone once, ordered by offset", () => {
    const list = zoneList();
    expect(new Set(list.map((z) => z.id)).size).toBe(list.length);
    for (let i = 1; i < list.length; i++) expect(list[i]!.minutes).toBeGreaterThanOrEqual(list[i - 1]!.minutes);
  });
});

describe("fold", () => {
  it("ignores case and accents so a search finds İstanbul and Zürich", () => {
    expect(fold("İstanbul")).toBe("istanbul");
    expect(fold("Zürich")).toBe("zurich");
    expect(fold("Kadıköy")).toBe("kadikoy");
    expect(fold("São Paulo")).toBe("sao paulo");
  });
});
