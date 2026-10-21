/** The pure part of the six-box code input (OtpInput.vue): which digits go into which box. */

/** Everything that is not a digit dropped: pasted "123 456", "123-456" and a keyboard's autofill all come out as digits. */
export function digitsOf(raw: string): string {
  return raw.replace(/\D/g, "");
}

/** Puts `raw`'s digits into `cells` from box `start` on (extra digits are dropped) and returns the new boxes and the box to focus next. */
export function spread(cells: readonly string[], start: number, raw: string): { cells: string[]; next: number } {
  const out = [...cells];
  let at = start;
  for (const d of digitsOf(raw)) {
    if (at >= out.length) break;
    out[at++] = d;
  }
  return { cells: out, next: at };
}

/** Where a paste of `count` digits begins: a whole code always starts at the first box, a partial one where the caret is. */
export function pasteStart(count: number, length: number, caret: number): number {
  return count >= length ? 0 : caret;
}

/** Empties one box. */
export function clearAt(cells: readonly string[], i: number): string[] {
  return cells.map((c, k) => (k === i ? "" : c));
}
