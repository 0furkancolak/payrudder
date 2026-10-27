import { describe, expect, it } from "vitest";
import { readSearchText } from "./useSearchValue";

describe("readSearchText", () => {
  it("reads text and what the router parsed out of it", () => {
    expect(readSearchText("ledger")).toBe("ledger");
    expect(readSearchText(123)).toBe("123");
    expect(readSearchText(true)).toBe("true");
  });

  it("gives undefined for nothing", () => {
    expect(readSearchText(undefined)).toBeUndefined();
    expect(readSearchText("")).toBeUndefined();
    expect(readSearchText({})).toBeUndefined();
  });
});
