import { describe, expect, it } from "vitest";
import { duration } from "../duration";

describe("duration", () => {
  it("picks the unit that reads best", () => {
    expect(duration(0, "en")).toBe("0s");
    expect(duration(45, "en")).toBe("45s");
    expect(duration(60, "en")).toBe("1m");
    expect(duration(45 * 60, "en")).toBe("45m");
    expect(duration(3 * 3600 + 20 * 60, "en")).toBe("3h");
    expect(duration(2 * 86_400 + 5 * 3600, "en")).toBe("2d");
  });
  it("rounds to the unit and reads in the language", () => {
    expect(duration(89, "en")).toBe("1m");
    expect(duration(90, "en")).toBe("2m");
    expect(duration(3 * 3600, "de")).toMatch(/^3/); // the unit is the language's own narrow form, whatever the ICU data of the runtime has
  });
});
