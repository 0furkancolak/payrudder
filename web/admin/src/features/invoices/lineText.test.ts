import { createI18n } from "vue-i18n";
import { describe, expect, it } from "vitest";
import en from "@/locales/en.json";
import tr from "@/locales/tr.json";
import { formatUtcDay } from "@/shared/lib/format";
import { lineText } from "./lineText";

const i18n = (locale: string) => createI18n({ legacy: false, locale, fallbackLocale: "en", messages: { en, tr } }).global;

describe("lineText", () => {
  const g = i18n("tr");
  const day = (v: string) => formatUtcDay(v, "tr", "day");
  const text = (d: string) => lineText(d, g.t as never, day);

  it("words a base fee line in the reader's language with the dates in their format", () => {
    const out = text("Base fee (monthly) × 2 seats — pro — 2026-10-27 to 2026-11-27");
    expect(out).not.toContain("Base fee");
    expect(out).not.toContain("2026-10-27");
    expect(out).toContain("pro");
    expect(out).toContain("2");
    expect(text("Base fee (yearly) — pro — 2026-10-27 to 2027-10-27")).not.toContain("Base fee");
  });

  it("accepts the ASCII x of older invoices", () => {
    expect(text("Base fee (monthly) x 2 seats — pro — 2026-10-27 to 2026-11-27")).not.toContain("Base fee");
  });

  it("words the other lines the billing service writes", () => {
    expect(text("Unused time on starter — 2026-10-27 to 2026-11-27")).not.toContain("Unused");
    expect(text("Coupon SPRING10")).toContain("SPRING10");
    expect(text("Coupon SPRING10")).not.toBe("Coupon SPRING10");
    expect(text("Usage — api_calls — 1200 calls")).toContain("api_calls");
    expect(text("Usage — api_calls — 1200 calls")).not.toContain("Usage");
    expect(text("Base fee")).not.toBe("Base fee");
  });

  it("leaves a description it does not know as written", () => {
    expect(text("KDV %20")).toBe("KDV %20");
    expect(text("Consulting, October")).toBe("Consulting, October");
    expect(lineText(undefined, g.t as never, day)).toBe("");
  });

  it("is English in English", () => {
    const e = i18n("en");
    expect(lineText("Coupon SPRING10", e.t as never, (v) => v)).toBe("Coupon SPRING10");
  });
});
