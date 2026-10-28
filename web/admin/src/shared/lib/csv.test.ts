import { describe, expect, it } from "vitest";
import { csvCell, toCsv } from "./csv";

describe("csv", () => {
  it("quotes what needs it and doubles quotes", () => {
    expect(csvCell("plain")).toBe("plain");
    expect(csvCell('a,"b"')).toBe('"a,""b"""');
    expect(csvCell("two\nlines")).toBe('"two\nlines"');
  });
  it("keeps a spreadsheet from running a cell as a formula, but leaves numbers alone", () => {
    expect(csvCell("=HYPERLINK(1)")).toBe("'=HYPERLINK(1)");
    expect(csvCell("@sum")).toBe("'@sum");
    expect(csvCell("+1 555")).toBe("'+1 555");
    expect(csvCell("-12.5")).toBe("-12.5");
    expect(csvCell(42)).toBe("42");
  });
  it("writes a header and rows with CRLF line ends", () => {
    expect(toCsv(["a", "b"], [["1", "x,y"]])).toBe('a,b\r\n1,"x,y"\r\n');
  });
});
