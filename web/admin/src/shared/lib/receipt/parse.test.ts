import { describe, expect, it } from "vitest";
import { FIXTURES } from "./fixtures";
import { amountToMinor, confident, LOW_CONFIDENCE, parseReceipt } from "./parse";

describe("amountToMinor", () => {
  it("reads both decimal conventions", () => {
    expect(amountToMinor("1.234,56", "TRY")).toBe(123456);
    expect(amountToMinor("1,234.56", "USD")).toBe(123456);
    expect(amountToMinor("49.00", "USD")).toBe(4900);
    expect(amountToMinor("49,5", "EUR")).toBe(4950);
    expect(amountToMinor("2 400,00", "EUR")).toBe(240000);
    expect(amountToMinor("1'250.00", "CHF")).toBe(125000);
  });
  it("tells a thousands separator from a decimal one", () => {
    expect(amountToMinor("1.250", "TRY")).toBe(125000);
    expect(amountToMinor("12,800", "JPY")).toBe(12800);
    expect(amountToMinor("1.250", "KWD")).toBe(1250);
    expect(amountToMinor("1.234.567", "USD")).toBe(123456700);
  });
  it("refuses more decimals than the currency has", () => {
    expect(amountToMinor("12.345", "JPY")).toBe(12345);
    expect(amountToMinor("1,5678", "USD")).toBeUndefined();
  });
});

describe("parseReceipt on real layouts", () => {
  it("an English invoice with a month-first date", () => {
    const r = parseReceipt(FIXTURES.usSaas);
    expect(r.vendor?.value).toBe("Acme Cloud, Inc.");
    expect(r.vendor?.confidence).toBeGreaterThanOrEqual(0.8);
    expect(r.date?.value).toBe("2026-10-26");
    expect(r.currency?.value).toBe("USD");
    expect(r.amountMinor?.value).toBe(12877);
    expect(r.taxMinor?.value).toBe(981);
    expect(confident(r.amountMinor)).toBe(true);
  });

  it("a Turkish e-Arşiv fatura: dotted date, comma decimals, KDV", () => {
    const r = parseReceipt(FIXTURES.trFatura);
    expect(r.vendor?.value).toBe("Mavi Bulut Bilişim Hizmetleri A.Ş.");
    expect(r.date?.value).toBe("2026-10-26");
    expect(r.currency?.value).toBe("TRY");
    expect(r.amountMinor?.value).toBe(150000);
    expect(r.taxMinor?.value).toBe(25000);
  });

  it("a German Rechnung", () => {
    const r = parseReceipt(FIXTURES.deRechnung);
    expect(r.vendor?.value).toBe("Nordlicht Software GmbH");
    expect(r.date?.value).toBe("2026-10-21");
    expect(r.currency?.value).toBe("EUR");
    expect(r.amountMinor?.value).toBe(142800);
    expect(r.taxMinor?.value).toBe(22800);
  });

  it("a French facture with a written month and spaced thousands", () => {
    const r = parseReceipt(FIXTURES.frFacture);
    expect(r.vendor?.value).toBe("Atelier Lumière SAS");
    expect(r.date?.value).toBe("2026-10-05");
    expect(r.amountMinor?.value).toBe(288000);
    expect(r.taxMinor?.value).toBe(48000);
  });

  it("a Spanish factura", () => {
    const r = parseReceipt(FIXTURES.esFactura);
    expect(r.date?.value).toBe("2026-10-03");
    expect(r.currency?.value).toBe("EUR");
    expect(r.amountMinor?.value).toBe(36300);
    expect(r.taxMinor?.value).toBe(6300);
  });

  it("noisy OCR of a till receipt: no company suffix, so the vendor is only a guess", () => {
    const r = parseReceipt(FIXTURES.ocrReceipt);
    expect(r.vendor?.value).toBe("BLUE BOTTLE COFFEE");
    expect(r.vendor?.confidence).toBeLessThan(LOW_CONFIDENCE);
    expect(r.date?.value).toBe("2026-10-24");
    expect(r.amountMinor?.value).toBe(1056);
    expect(r.taxMinor?.value).toBe(81);
  });

  it("a currency with no decimals", () => {
    const r = parseReceipt(FIXTURES.jpInvoice);
    expect(r.currency?.value).toBe("JPY");
    expect(r.amountMinor?.value).toBe(12800);
    expect(r.date?.value).toBe("2026-10-02");
  });
});

describe("parseReceipt edge cases", () => {
  it("never reads a percentage, a tax number or the due date as what they are not", () => {
    const r = parseReceipt(`Foo Ltd.\nVAT no: GB123456789\nDue date 01/11/2026\nInvoice date 21/10/2026\nVAT 20% 20.00\nTotal 120.00 GBP`);
    expect(r.date?.value).toBe("2026-10-21");
    expect(r.taxMinor?.value).toBe(2000);
    expect(r.amountMinor?.value).toBe(12000);
  });

  it("an ambiguous slash date is reported with less confidence", () => {
    const eur = parseReceipt("Foo GmbH\nDatum 03/04/2026\nTotal 10,00 EUR");
    expect(eur.date?.value).toBe("2026-04-03");
    expect(eur.date!.confidence).toBeLessThan(0.9);
  });

  it("falls back to the default currency, with low confidence, and to the file name for a vendor", () => {
    const r = parseReceipt("12/31/2026\nTotal 40.00", { defaultCurrency: "EUR", filename: "adobe-creative-cloud_2026-10.pdf" });
    expect(r.currency).toEqual({ value: "EUR", confidence: 0.3 });
    expect(r.vendor?.value).toBe("adobe creative cloud");
    expect(r.vendor!.confidence).toBeLessThan(LOW_CONFIDENCE);
  });

  it("returns nothing for empty text and never throws on junk", () => {
    expect(parseReceipt("")).toEqual({});
    expect(() => parseReceipt("\u0000\u0001 ¿¿¿ 99999999999999999999999 1.2.3.4.5")).not.toThrow();
  });
});
