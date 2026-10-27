import { describe, expect, it } from "vitest";
import { formatCount, formatDate, formatPercent, formatUtcDay, isolateDate } from "./format";

describe("formatDate", () => {
  it("gives the empty text for nothing and for an unreadable date instead of throwing", () => {
    expect(formatDate(undefined, "en")).toBe("");
    expect(formatDate(null, "en", "day", "—")).toBe("—");
    expect(formatDate("", "en", "day", "—")).toBe("—");
    expect(formatDate("not a date", "en", "dateTime", "—")).toBe("—");
  });

  it("formats an instant in the style asked for", () => {
    const at = "2026-10-21T12:30:00Z";
    expect(formatDate(at, "en-US", "day")).toBe(new Intl.DateTimeFormat("en-US", { dateStyle: "medium" }).format(new Date(at)));
    expect(formatDate(at, "en-US", "dateTime")).toContain(":");
    expect(formatDate(at, "en-US", "monthDay")).not.toContain("2026");
  });
});

describe("formatUtcDay", () => {
  it("keeps the calendar day whatever the reader's zone is", () => {
    expect(formatUtcDay("2026-10-21", "en-US", "day")).toBe("Oct 21, 2026");
    expect(formatUtcDay("2026-10-21", "en-US", "monthDay")).toBe("Oct 21");
    expect(formatUtcDay("2026-10-01", "en-US", "month")).toBe("Oct 2026");
  });

  it("gives the empty text for nothing", () => {
    expect(formatUtcDay("", "en", "day", "—")).toBe("—");
    expect(formatUtcDay("garbage", "en", "day", "—")).toBe("—");
  });
});

describe("counts and percentages", () => {
  it("groups the digits of a count and shows a missing one as zero", () => {
    expect(formatCount(1234567, "en-US")).toBe("1,234,567");
    expect(formatCount(undefined, "en-US")).toBe("0");
  });

  it("shows one decimal at most, and a dash for no value", () => {
    expect(formatPercent(12.34, "en-US")).toBe("12.3%");
    expect(formatPercent(null, "en-US")).toBe("—");
  });
});

describe("dates in right-to-left languages", () => {
  it("are isolated so they keep their order inside Latin text", () => {
    expect(isolateDate("27 Oct 2026", "ar")).toBe("\u206627 Oct 2026\u2069");
    expect(formatDate("2026-10-27T12:00:00Z", "ar", "day").startsWith("\u2066")).toBe(true);
    expect(formatUtcDay("2026-10-27", "ar-EG", "day").endsWith("\u2069")).toBe(true);
  });

  it("leave left-to-right languages unchanged", () => {
    expect(isolateDate("27 Oct 2026", "en")).toBe("27 Oct 2026");
    expect(formatUtcDay("2026-10-21", "tr", "day")).not.toContain("\u2066");
  });
});
