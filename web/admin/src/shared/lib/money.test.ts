import { describe, expect, it } from "vitest";
import { currencyInfo } from "./intl";
import { formatMinor, maskDigits, maskEmail, minorDigits, minorToMajor, parseMajor } from "./money";
import { privacyMode } from "./prefs";

describe("minorToMajor", () => {
  it("is the plain decimal a person would type", () => {
    expect(minorToMajor("1250", "USD")).toBe("12.50");
    expect(minorToMajor("5", "USD")).toBe("0.05");
    expect(minorToMajor("0", "USD")).toBe("0.00");
    expect(minorToMajor(900, "JPY")).toBe("900");
    expect(minorToMajor("1234", "KWD")).toBe("1.234");
    expect(minorToMajor("9007199254740993", "USD")).toBe("90071992547409.93");
  });
  it("round-trips through parseMajor", () => {
    for (const m of ["1", "99", "100", "123456"]) expect(parseMajor(minorToMajor(m, "TRY"), "TRY")).toBe(m);
  });
  it("gives nothing for what is not a non-negative integer", () => {
    expect(minorToMajor("-5", "USD")).toBe("");
    expect(minorToMajor("1.5", "USD")).toBe("");
    expect(minorToMajor(undefined, "USD")).toBe("");
  });
});

describe("exponents come from the backend table, not from Intl", () => {
  it("formats HUF and IDR with two digits and IQD with three, as pkg/money does", () => {
    expect(formatMinor("150000", "HUF", "en")).toMatch(/1,500\.00/);
    expect(formatMinor("150000", "IDR", "en")).toMatch(/1,500\.00/);
    expect(formatMinor("1500000", "IQD", "en")).toMatch(/1,500\.000/);
  });
  it("parses and prints typed amounts with those exponents", () => {
    expect(parseMajor("1500", "HUF")).toBe("150000");
    expect(parseMajor("1500", "IQD")).toBe("1500000");
    expect(minorToMajor("150000", "HUF")).toBe("1500.00");
    expect(minorDigits("COP")).toBe(2);
    expect(minorDigits("JPY")).toBe(0);
    expect(minorDigits("KWD")).toBe(3);
  });
  it("reports the same digits for the currency picker", () => {
    expect(currencyInfo("HUF", "en").digits).toBe(2);
    expect(currencyInfo("IQD", "en").digits).toBe(3);
  });
});

describe("formatMinor", () => {
  it("uses the currency's minor-unit exponent", () => {
    expect(formatMinor("1000", "TRY", "en")).toMatch(/10\.00/);
    expect(formatMinor("1000", "JPY", "en")).toMatch(/1,000/);
    expect(formatMinor("1234", "KWD", "en")).toMatch(/1\.234/);
  });
  it("keeps a large amount exact", () => {
    expect(formatMinor("9007199254740993", "USD", "en")).toContain("90,071,992,547,409.93");
  });
  it("follows the locale", () => {
    expect(formatMinor("123456", "EUR", "de")).toContain("1.234,56");
  });
  it("passes through what it cannot format", () => {
    expect(formatMinor("12", undefined, "en")).toBe("12");
    expect(formatMinor("abc", "USD", "en")).toBe("abc");
  });
  it("handles negative amounts (credits)", () => {
    expect(formatMinor("-250", "USD", "en")).toContain("2.50");
    expect(formatMinor("-250", "USD", "en")).toContain("-");
  });
});

describe("parseMajor", () => {
  it("moves the digits as text into minor units", () => {
    expect(parseMajor("12.5", "USD")).toBe("1250");
    expect(parseMajor("12,50", "EUR")).toBe("1250");
    expect(parseMajor("1 250.00", "TRY")).toBe("125000");
    expect(parseMajor("0", "USD")).toBe("0");
    expect(parseMajor("0.05", "USD")).toBe("5");
    expect(parseMajor("900", "JPY")).toBe("900");
    expect(parseMajor("1.234", "KWD")).toBe("1234");
    expect(parseMajor("90071992547409.93", "USD")).toBe("9007199254740993");
  });
  it("refuses what is not an amount in that currency", () => {
    expect(parseMajor("", "USD")).toBeNull();
    expect(parseMajor("abc", "USD")).toBeNull();
    expect(parseMajor("-1", "USD")).toBeNull();
    expect(parseMajor("1.005", "USD")).toBeNull();
    expect(parseMajor("1.5", "JPY")).toBeNull();
    expect(parseMajor("1.2.3", "USD")).toBeNull();
  });
});

describe("formatMinor in a right-to-left language", () => {
  it("isolates the amount so the sign and currency stay beside the digits", () => {
    const out = formatMinor("-3000", "TRY", "ar");
    expect(out.startsWith("⁦")).toBe(true);
    expect(out.endsWith("⁩")).toBe(true);
    expect(formatMinor("-3000", "TRY", "en")).not.toContain("⁦");
  });
});

describe("privacy mode", () => {
  it("masks the figures of an amount and keeps the currency", () => {
    privacyMode.value = true;
    try {
      expect(formatMinor("123456", "USD", "en")).toBe("$••••");
      expect(formatMinor("-3000", "TRY", "en")).not.toMatch(/\d/);
      expect(formatMinor("-3000", "TRY", "en")).toContain("•");
      expect(formatMinor("900", "JPY", "en")).toBe("¥••••");
      expect(formatMinor("abc", "USD", "en")).toBe("abc");
    } finally {
      privacyMode.value = false;
    }
    expect(formatMinor("123456", "USD", "en")).toBe("$1,234.56");
  });
  it("masks other numeral systems", () => {
    expect(maskDigits("١٢٣٫٤٥ ر.س.")).toBe("•••• ر.س.");
    expect(maskDigits("1 234,56 €")).toBe("•••• €");
  });
  it("masks an e-mail address only in privacy mode", () => {
    expect(maskEmail("ann@example.com")).toBe("ann@example.com");
    privacyMode.value = true;
    try {
      expect(maskEmail("ann@example.com")).toBe("a•••@example.com");
    } finally {
      privacyMode.value = false;
    }
  });
});
