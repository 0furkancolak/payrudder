import { describe, expect, it } from "vitest";
import { durationHours, formatDuration, graceDuration } from "./duration";

describe("durations", () => {
  it("writes hours as the API's duration", () => {
    expect(graceDuration(72)).toBe("259200s");
    expect(graceDuration(0)).toBe("0s");
  });

  it("reads the API's duration back as hours", () => {
    expect(durationHours("259200s")).toBe(72);
    expect(durationHours("5400s")).toBe(1.5);
    expect(durationHours("3600.000s")).toBe(1);
    expect(durationHours("soon")).toBeNull();
    expect(durationHours(undefined)).toBeNull();
  });

  it("shows hours instead of 259200s", () => {
    expect(formatDuration("259200s", "en-US")).toBe("72 hours");
    expect(formatDuration("3600s", "en-US")).toBe("1 hour");
    expect(formatDuration("odd", "en-US")).toBe("odd");
    expect(formatDuration(undefined, "en-US")).toBe("");
  });
});
