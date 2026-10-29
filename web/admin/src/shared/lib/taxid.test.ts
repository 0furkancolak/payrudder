import { describe, expect, it } from "vitest";
import { normalizeTaxId, taxIdHint, validEuVatShape, validTckn, validVkn } from "./taxid";

// The vectors of the two Turkish checksums are the ones of pkg/taxid (Go), produced and judged by python-stdnum: an implementation that is neither of ours.
const VKN: [string, boolean][] = [
  ["3667127684", true],
  ["2233079244", true],
  ["1177774121", true],
  ["8072902227", true],
  ["7706589481", true],
  ["4504000883", true],
  ["4499207074", true],
  ["8341704718", true],
  ["5440910317", true],
  ["5164388317", true],
  ["5260181590", false],
  ["8301661318", false],
  ["6091390996", false],
  ["0308246281", false],
  ["9482199351", false],
  ["8190937865", false],
  ["7975432319", false],
  ["4875749118", false],
  ["6252760189", false],
  ["5559797114", false],
  ["7104974650", false],
  ["7529170342", false],
  ["2684656321", false],
  ["0268599528", false],
  ["5667127684", false],
  ["0233079244", false],
  ["1175774121", false],
  ["8072912227", false],
  ["7706989481", false],
  ["4604000883", false],
  ["", false],
  ["123456789", false],
  ["12345678901", false],
  ["123456789a", false],
];
const TCKN: [string, boolean][] = [
  ["11995305520", true],
  ["92812646446", true],
  ["36670009494", true],
  ["13453709710", true],
  ["95986365168", true],
  ["29072637792", true],
  ["95113115444", true],
  ["10758669428", true],
  ["40394525642", true],
  ["34578364820", true],
  ["49931582794", false],
  ["44093056529", false],
  ["51307871616", false],
  ["14956605366", false],
  ["40626116957", false],
  ["32008261995", false],
  ["92254282116", false],
  ["83420750961", false],
  ["33969372930", false],
  ["78265123308", false],
  ["15169784649", false],
  ["46657872009", false],
  ["87379727611", false],
  ["35651788002", false],
  ["11595305520", false],
  ["92812646456", false],
  ["36640009494", false],
  ["13453700710", false],
  ["95986565168", false],
  ["29372637792", false],
  ["00000000000", false],
  ["1234567890", false],
  ["123456789012", false],
  ["1000000014a", false],
];

describe("Turkish tax numbers", () => {
  it.each(VKN)("VKN %s is %s", (id, ok) => expect(validVkn(id)).toBe(ok));
  it.each(TCKN)("TCKN %s is %s", (id, ok) => expect(validTckn(id)).toBe(ok));
  it("ignores what people type for readability", () => {
    expect(normalizeTaxId("de 136.695-976")).toBe("DE136695976");
    expect(validTckn("100 000 001 46")).toBe(true);
    expect(validVkn("123-456-7890")).toBe(true);
  });
});

describe("European VAT ids", () => {
  it("accepts the shape of a good id and refuses the rest", () => {
    for (const id of ["DE136695976", "FR40303265045", "ESX1234567L", "NL004495445B01", "ATU12345675", "IT00743110157", "de136695976", "DE 136 695 976"]) expect(validEuVatShape(id)).toBe(true);
    for (const id of ["DE12345", "FR1", "US123456789", "", "DE13669597a", "DE1366959761"]) expect(validEuVatShape(id)).toBe(false);
  });
});

describe("taxIdHint: a hint, never a refusal", () => {
  it("says nothing for an empty id, for a good one, and where it has no opinion", () => {
    expect(taxIdHint("", "TR", "business")).toBeNull();
    expect(taxIdHint("1234567890", "TR", "business")).toBeNull();
    expect(taxIdHint("10000000146", "TR", "individual")).toBeNull();
    expect(taxIdHint("anything", "US", "business")).toBeNull();
    expect(taxIdHint("anything", "DE", "individual")).toBeNull();
    expect(taxIdHint("whatever", undefined, undefined)).toBeNull();
  });
  it("tells a Turkish business from an individual by the number", () => {
    expect(taxIdHint("10000000146", "TR", "business")).toBe("tr_is_tckn");
    expect(taxIdHint("1234567890", "TR", "individual")).toBe("tr_is_vkn");
    expect(taxIdHint("12345", "TR", "business")).toBe("tr_business");
    expect(taxIdHint("12345", "TR", "individual")).toBe("tr_individual");
    expect(taxIdHint("12345", "TR", "")).toBe("tr_any");
    expect(taxIdHint("1234567890", "TR", "")).toBeNull();
  });
  it("checks the shape of a VAT id of a European business", () => {
    expect(taxIdHint("DE136695976", "DE", "business")).toBeNull();
    expect(taxIdHint("DE12", "DE", "business")).toBe("eu_vat");
    expect(taxIdHint("GR", "GR", "business")).toBe("eu_vat");
  });
});
