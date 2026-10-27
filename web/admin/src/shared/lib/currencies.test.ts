import { describe, expect, it } from "vitest";
import { currencyOfZone } from "./currencies";

describe("currencyOfZone", () => {
  it("guesses the currency of a zone whose currency is clear", () => {
    expect(currencyOfZone("Europe/Istanbul")).toBe("TRY");
    expect(currencyOfZone("Europe/Berlin")).toBe("EUR");
    expect(currencyOfZone("America/New_York")).toBe("USD");
    expect(currencyOfZone("Asia/Tokyo")).toBe("JPY");
  });

  it("says nothing for a zone it does not know", () => {
    expect(currencyOfZone("UTC")).toBeUndefined();
    expect(currencyOfZone("Mars/Phobos")).toBeUndefined();
    expect(currencyOfZone(undefined)).toBeUndefined();
  });
});
