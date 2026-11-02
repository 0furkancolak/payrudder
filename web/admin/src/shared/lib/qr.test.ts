import { describe, expect, it } from "vitest";
import { QUIET_ZONE, qrMatrix, qrShape } from "./qr";

const URI = "otpauth://totp/Payrudder:ada@example.com?secret=JBSWY3DPEHPK3PXP&issuer=Payrudder";

/** The dark modules a path of `h` runs describes, to compare with the matrix it was made from. */
function drawn(path: string, size: number): boolean[][] {
  const grid = Array.from({ length: size }, () => Array.from({ length: size }, () => false));
  for (const m of path.matchAll(/M(\d+) (\d+)h(\d+)v1h-\d+z/g)) {
    const [x, y, w] = [Number(m[1]), Number(m[2]), Number(m[3])];
    for (let i = 0; i < w; i++) (grid[y] as boolean[])[x + i] = true;
  }
  return grid;
}

describe("qrShape", () => {
  it("has the module count of the version the text needs (81 bytes at level M is version 5: 37 modules) plus the quiet zone on both sides", () => {
    const s = qrShape(URI);
    expect(s.modules).toBe(37);
    expect(s.size).toBe(37 + 2 * QUIET_ZONE);
    expect(qrShape("hello").modules).toBe(21);
  });

  it("is the same drawing every time for the same text, and another for another text", () => {
    expect(qrShape(URI)).toEqual(qrShape(URI));
    expect(qrShape(URI).path).not.toBe(qrShape(URI + "x").path);
  });

  it("draws exactly the dark modules of the matrix, inside the quiet zone", () => {
    const s = qrShape(URI);
    const grid = drawn(s.path, s.size);
    const m = qrMatrix(URI);
    for (let r = 0; r < s.size; r++) {
      for (let c = 0; c < s.size; c++) {
        const inside = r >= QUIET_ZONE && c >= QUIET_ZONE && r < QUIET_ZONE + s.modules && c < QUIET_ZONE + s.modules;
        expect(grid[r]?.[c], `${r},${c}`).toBe(inside ? m[r - QUIET_ZONE]?.[c - QUIET_ZONE] : false);
      }
    }
  });

  it("has the three finder squares: a dark 7x7 ring in every corner but the bottom right", () => {
    const m = qrMatrix(URI);
    const n = m.length;
    for (const [r0, c0] of [[0, 0], [0, n - 7], [n - 7, 0]] as const) {
      for (let i = 0; i < 7; i++) {
        expect(m[r0]?.[c0 + i]).toBe(true);
        expect(m[r0 + 6]?.[c0 + i]).toBe(true);
        expect(m[r0 + i]?.[c0]).toBe(true);
        expect(m[r0 + i]?.[c0 + 6]).toBe(true);
      }
      expect(m[r0 + 3]?.[c0 + 3]).toBe(true);
      expect(m[r0 + 1]?.[c0 + 1]).toBe(false);
    }
  });
});
