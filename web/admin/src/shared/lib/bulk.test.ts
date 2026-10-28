import { describe, expect, it } from "vitest";
import { runBulk } from "./bulk";

describe("runBulk", () => {
  it("acts on every row in order and reports the ones that failed without stopping", async () => {
    const seen: number[] = [];
    const r = await runBulk([1, 2, 3, 4], async (n) => {
      seen.push(n);
      if (n % 2 === 0) throw new Error(`no ${n}`);
    });
    expect(seen).toEqual([1, 2, 3, 4]);
    expect(r.ok).toEqual([1, 3]);
    expect(r.failed.map((f) => f.row)).toEqual([2, 4]);
  });
});
