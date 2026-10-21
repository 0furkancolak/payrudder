import { describe, expect, it } from "vitest";
import { checkIban, normaliseIban } from "./iban";

describe("normaliseIban", () => {
  it("drops spaces and dashes and upper-cases", () => {
    expect(normaliseIban(" de89 3704-0044 0532 0130 00 ")).toBe("DE89370400440532013000");
    expect(normaliseIban("tr330006100519786457841326")).toBe("TR330006100519786457841326");
  });
});

describe("checkIban", () => {
  it.each([
    "DE89 3704 0044 0532 0130 00",
    "TR33 0006 1005 1978 6457 8413 26",
    "GB82 WEST 1234 5698 7654 32",
    "FR14 2004 1010 0505 0001 3M02 606",
    "NL91-ABNA-0417-1643-00",
    "NO93 8601 1117 947",
  ])("accepts %s", (input) => {
    const r = checkIban(input);
    expect(r.ok).toBe(true);
    if (r.ok) expect(r.iban).toBe(normaliseIban(input));
  });

  it("accepts lower case as typed", () => {
    expect(checkIban("de89370400440532013000")).toEqual({ ok: true, iban: "DE89370400440532013000" });
  });

  it("refuses a wrong check digit or a swapped digit", () => {
    expect(checkIban("DE88 3704 0044 0532 0130 00")).toEqual({ ok: false, problem: "checksum" });
    expect(checkIban("DE89 3704 0044 0532 0130 01")).toEqual({ ok: false, problem: "checksum" });
    expect(checkIban("TR33 0006 1005 1978 6457 8413 62")).toEqual({ ok: false, problem: "checksum" });
  });

  it("refuses nothing, too short, too long and wrong shape", () => {
    expect(checkIban("  ")).toEqual({ ok: false, problem: "empty" });
    expect(checkIban("DE89 3704 0044")).toEqual({ ok: false, problem: "length" });
    expect(checkIban("DE89" + "0".repeat(31))).toEqual({ ok: false, problem: "length" });
    expect(checkIban("1289 3704 0044 0532 0130 00")).toEqual({ ok: false, problem: "format" });
    expect(checkIban("DE89 3704 0044 0532 0130 0!")).toEqual({ ok: false, problem: "format" });
  });
});
