import { describe, expect, it } from "vitest";
import { localizePercent, parsePercent, percentText } from "./api";

describe("a tax rate typed with the reader's decimal separator", () => {
  it("is shown back with the same separator and stored as the same basis points", () => {
    expect(parsePercent("20,5")).toBe(2050);
    expect(localizePercent(percentText(2050), "tr")).toBe("20,5");
    expect(localizePercent(percentText(2050), "en")).toBe("20.5");
    expect(parsePercent(localizePercent(percentText(825), "de"))).toBe(825);
  });
  it("leaves a whole rate alone", () => {
    expect(localizePercent(percentText(2000), "tr")).toBe("20");
  });
});
