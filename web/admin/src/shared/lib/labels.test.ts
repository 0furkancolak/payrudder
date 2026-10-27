import { describe, expect, it } from "vitest";
import { capFirst } from "./labels";

describe("capFirst", () => {
  it("capitalises the first letter by the rules of the language", () => {
    expect(capFirst("ödendi", "tr")).toBe("Ödendi");
    expect(capFirst("iptal", "tr")).toBe("İptal");
    expect(capFirst("paid", "en")).toBe("Paid");
    expect(capFirst("", "en")).toBe("");
  });
});
