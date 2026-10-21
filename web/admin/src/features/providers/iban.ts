/** What is wrong with an IBAN the operator typed; the form shows a translated message per reason. */
export type IbanProblem = "empty" | "length" | "format" | "checksum";

/** Strips spaces and dashes and upper-cases: the form the server stores and prints ("tr33 0006 1005 ..." -> "TR33000610..."). */
export function normaliseIban(input: string): string {
  return input.replace(/[\s-]/g, "").toUpperCase();
}

/** The remainder of the digits of an IBAN (letters as 10..35) divided by 97, computed in chunks so no number gets large. */
function mod97(rearranged: string): number {
  let rest = 0;
  for (const ch of rearranged) {
    const value = ch >= "A" ? String(ch.charCodeAt(0) - 55) : ch;
    for (const digit of value) rest = (rest * 10 + Number(digit)) % 97;
  }
  return rest;
}

/** Checks an IBAN with ISO 7064 mod 97-10 after normalising it: 15 to 34 characters, two letters, two check digits, then letters and digits. */
export function checkIban(input: string): { ok: true; iban: string } | { ok: false; problem: IbanProblem } {
  const iban = normaliseIban(input);
  if (iban === "") return { ok: false, problem: "empty" };
  if (iban.length < 15 || iban.length > 34) return { ok: false, problem: "length" };
  if (!/^[A-Z]{2}\d{2}[A-Z0-9]+$/.test(iban)) return { ok: false, problem: "format" };
  if (mod97(iban.slice(4) + iban.slice(0, 4)) !== 1) return { ok: false, problem: "checksum" };
  return { ok: true, iban };
}
