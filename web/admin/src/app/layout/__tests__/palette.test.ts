import { describe, expect, it } from "vitest";
import { matches } from "../palette";

describe("the command palette's matching", () => {
  it("ignores case and accents, Turkish letters included", () => {
    expect(matches("odeme", "Ödemeler")).toBe(true);
    expect(matches("MUSTERI", "Müşteriler")).toBe(true);
    expect(matches("isik", "Işık")).toBe(true);
    expect(matches("cafe", "Café")).toBe(true);
  });

  it("needs every word, in any order, and an empty query finds everything", () => {
    expect(matches("links payment", "Payment links")).toBe(true);
    expect(matches("payment keys", "Payment links")).toBe(false);
    expect(matches("  ", "Customers")).toBe(true);
  });
});
