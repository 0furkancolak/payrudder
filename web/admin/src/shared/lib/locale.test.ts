import { describe, expect, it } from "vitest";
import { toApiLocale } from "./locale";

describe("toApiLocale", () => {
  it("sends the language the page speaks, not always English", () => {
    expect(toApiLocale("tr")).toBe("tr");
    expect(toApiLocale("ar")).toBe("ar");
    expect(toApiLocale("de-AT")).toBe("de");
  });

  it("falls back to English for a language the API does not know", () => {
    expect(toApiLocale("ja")).toBe("en");
    expect(toApiLocale("")).toBe("en");
  });
});
