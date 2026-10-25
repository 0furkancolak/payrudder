import { describe, expect, it } from "vitest";
import { toLocalInput, todayYmd, ym, ymd } from "./dates";

describe("calendar days", () => {
  const d = new Date(2026, 9, 5, 7, 3); // 5 October 2026, 07:03, in whatever zone the test runs in

  it("pads days and months", () => {
    expect(ymd(d)).toBe("2026-10-05");
    expect(todayYmd(d)).toBe("2026-10-05");
    expect(ym(d)).toBe("2026-10");
    expect(ymd(new Date(2026, 0, 1))).toBe("2026-01-01");
  });

  it("writes a date for a datetime-local input", () => {
    expect(toLocalInput(d)).toBe("2026-10-05T07:03");
  });

  it("agrees with the en-CA trick it replaces", () => {
    expect(todayYmd(d)).toBe(d.toLocaleDateString("en-CA"));
  });
});
