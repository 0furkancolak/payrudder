import { describe, expect, it } from "vitest";
import { normalizeTaxId, taxIdHint, validEuVat, validEuVatShape, validTckn, validVkn } from "../taxid";

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

// The vectors of the VAT checksums are the ones of pkg/taxid (Go), produced and judged by python-stdnum (stdnum.eu.vat): the valid ones are the examples of its
// documentation or numbers found by searching and judged valid by it, the invalid ones are those of its documentation or a valid one with one character changed that it
// judges invalid. Every one of them has the shape of a VAT id, so a false is the checksum's doing. A country with no checksum here (SK, XI) has no vector.
const EU_VAT_VECTORS: [string, boolean][] = [
  ["ATU13585627", true],
  ["ATU91621601", true],
  ["ATU13585628", false],
  ["ATU13585637", false],
  ["ATU91621602", false],
  ["BE0428759497", true],
  ["BE0403019261", true],
  ["BE0428759498", false],
  ["BE0428759407", false],
  ["BE0403019262", false],
  ["BG175074752", true],
  ["BG8973928695", true],
  ["BG175074753", false],
  ["BG175074762", false],
  ["CY10259033P", true],
  ["CY95388011J", true],
  ["CY10259033Q", false],
  ["CY10259034P", false],
  ["CY95388011K", false],
  ["CZ25123891", true],
  ["CZ640903926", true],
  ["CZ25123892", false],
  ["CZ25123801", false],
  ["CZ640903927", false],
  ["DE136695976", true],
  ["DE188007596", true],
  ["DE136695978", false],
  ["DE136695977", false],
  ["DE136695986", false],
  ["DE188007597", false],
  ["DK13585628", true],
  ["DK16913898", true],
  ["DK13585629", false],
  ["DK13585638", false],
  ["DK16913899", false],
  ["EE100594102", true],
  ["EE100931558", true],
  ["EE100594103", false],
  ["EE100594112", false],
  ["EE100931559", false],
  ["EL094259216", true],
  ["EL498861492", true],
  ["EL123456781", false],
  ["EL094259217", false],
  ["EL094259226", false],
  ["EL498861493", false],
  ["ESB64717838", true],
  ["ES54362315K", true],
  ["ESX5253868R", true],
  ["ESM1234567L", true],
  ["ESB64717839", false],
  ["ESB64717848", false],
  ["ES54362315L", false],
  ["ESX5253868S", false],
  ["ESM1234567M", false],
  ["FI20774740", true],
  ["FI45170724", true],
  ["FI20774741", false],
  ["FI20774750", false],
  ["FI45170725", false],
  ["FR23334175221", true],
  ["FR40303265045", true],
  ["FRK7399859412", true],
  ["FR4Z123456782", true],
  ["FR84323140391", false],
  ["FRIO334175221", false],
  ["FR23334175222", false],
  ["FR23334175231", false],
  ["FR40303265046", false],
  ["FRK7399859413", false],
  ["HR33392005961", true],
  ["HR58626137648", true],
  ["HR33392005962", false],
  ["HR33392005971", false],
  ["HR58626137649", false],
  ["HU12892312", true],
  ["HU04412427", true],
  ["HU12892313", false],
  ["HU12892322", false],
  ["HU04412428", false],
  ["IE6433435F", true],
  ["IE6433435OA", true],
  ["IE8D79739I", true],
  ["IE6433435G", false],
  ["IE6433436F", false],
  ["IE6433435OB", false],
  ["IE8D79739J", false],
  ["IT00743110157", true],
  ["IT59576010843", true],
  ["IT00743110158", false],
  ["IT00743110167", false],
  ["IT59576010844", false],
  ["LT119511515", true],
  ["LT100001919017", true],
  ["LT100004801610", true],
  ["LT119511516", false],
  ["LT119511525", false],
  ["LT100001919018", false],
  ["LT100004801611", false],
  ["LU15027442", true],
  ["LU82289501", true],
  ["LU15027443", false],
  ["LU15027452", false],
  ["LU82289502", false],
  ["LV40003521600", true],
  ["LV22069520547", true],
  ["LV40003521601", false],
  ["LV40003521610", false],
  ["MT11679112", true],
  ["MT60282319", true],
  ["MT11679113", false],
  ["MT11679122", false],
  ["MT60282310", false],
  ["NL004495445B01", true],
  ["NL002455799B11", true],
  ["NL123456789B90", false],
  ["NL004495545B01", false],
  ["NL002455899B11", false],
  ["PL8567346215", true],
  ["PL6161379700", true],
  ["PL8567346216", false],
  ["PL8567346225", false],
  ["PL6161379701", false],
  ["PT501964843", true],
  ["PT204433568", true],
  ["PT501964844", false],
  ["PT501964853", false],
  ["PT204433569", false],
  ["RO18547290", true],
  ["RO70138", true],
  ["RO18547291", false],
  ["RO18547200", false],
  ["RO70139", false],
  ["SE123456789701", true],
  ["SE927293143801", true],
  ["SE123456789101", false],
  ["SE123456789801", false],
  ["SE123456889701", false],
  ["SE927293143901", false],
  ["SI50223054", true],
  ["SI96862335", true],
  ["SI50223055", false],
  ["SI50223064", false],
  ["SI96862336", false],
];

// Numbers python-stdnum judges invalid (the second of each pair) that are accepted, because the branch they belong to is shape-only: SK and XI (no algorithm here) and the
// numbers made of a person's birth date (CZ with 9 or 10 digits, BG with 10, the personal codes of LV). A warning on a good number is worse than none on a bad one.
const WITHOUT_CHECKSUM = ["SK2022749619", "SK2022749610", "XI123456782", "XI123456783", "CZ7103192745", "CZ7103192746", "BG7523169263", "BG7523169264", "LV16117519997", "LV16137519997"];

describe("European VAT ids", () => {
  it.each(EU_VAT_VECTORS)("VAT id %s is %s", (id, ok) => {
    expect(validEuVatShape(id)).toBe(true);
    expect(validEuVat(id)).toBe(ok);
  });
  it("accepts what has no checksum here, by its shape", () => {
    for (const id of WITHOUT_CHECKSUM) expect(validEuVat(id)).toBe(true);
  });
  it("ignores what people type for readability, and refuses a wrong shape or checksum", () => {
    expect(validEuVat("de 136.695-976")).toBe(true);
    for (const id of ["DE 136 695 978", "DE12345", "US136695976", ""]) expect(validEuVat(id)).toBe(false);
  });
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
  it("checks the check digits of a VAT id where they are known, and only warns", () => {
    expect(taxIdHint("DE136695978", "DE", "business")).toBe("eu_vat");
    expect(taxIdHint("FR84323140391", "FR", "business")).toBe("eu_vat");
    expect(taxIdHint("EL094259216", "GR", "business")).toBeNull();
    expect(taxIdHint("SK2022749610", "SK", "business")).toBeNull();
    expect(taxIdHint("DE136695978", "DE", "individual")).toBeNull();
  });
});
