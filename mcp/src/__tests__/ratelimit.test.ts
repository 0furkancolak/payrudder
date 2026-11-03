import { describe, expect, it } from "vitest";
import { createRateLimit, limitKeyOf } from "../ratelimit.js";

describe("the limit per key", () => {
  it("lets a burst through, then says how many seconds to wait, and refills over time", () => {
    let t = 0;
    const l = createRateLimit(60, () => t)!; // a call a second on average, a burst of 10
    const taken = Array.from({ length: 12 }, () => l.take("k"));
    expect(taken.slice(0, 10)).toEqual(Array(10).fill(0));
    expect(taken.slice(10)).toEqual([1, 1]); // refused calls cost nothing: the wait does not grow
    t += 1000;
    expect(l.take("k")).toBe(0);
    expect(l.take("k")).toBe(1);
    t += 60_000;
    expect(Array.from({ length: 10 }, () => l.take("k"))).toEqual(Array(10).fill(0)); // never more than the burst, however long it was idle
    expect(l.take("k")).toBe(1);
  });

  it("waits longer when calls are rarer: 6 a minute is a call every 10 seconds", () => {
    let t = 0;
    const l = createRateLimit(6, () => t)!;
    expect(l.take("k")).toBe(0);
    expect(l.take("k")).toBe(10);
    t += 4000;
    expect(l.take("k")).toBe(6);
  });

  it("counts each key on its own, and is off for 0 or a value that is not a positive number", () => {
    const l = createRateLimit(6, () => 0)!;
    expect(l.take("a")).toBe(0);
    expect(l.take("a")).toBeGreaterThan(0);
    expect(l.take("b")).toBe(0);
    for (const off of [0, -1, NaN]) expect(createRateLimit(off)).toBeUndefined();
  });

  it("forgets the key idle the longest when it is sent more keys than it keeps, and never grows without end", () => {
    const l = createRateLimit(6, () => 0)!;
    expect(l.take("victim")).toBe(0);
    expect(l.take("victim")).toBeGreaterThan(0);
    for (let i = 0; i < 10_001; i++) l.take(`flood-${i}`);
    expect(l.take("victim")).toBe(0); // forgotten, so it starts full again: the flood cannot make a real key wait
  });

  it("names a key by a hash of the whole of it, so the public prefix is not enough to spend its share", () => {
    expect(limitKeyOf("qk_test_ab12cd34_SECRET")).not.toBe(limitKeyOf("qk_test_ab12cd34_OTHER"));
    expect(limitKeyOf("qk_test_ab12cd34_SECRET")).toMatch(/^[0-9a-f]{64}$/);
    expect(limitKeyOf("qk_test_ab12cd34_SECRET")).not.toContain("SECRET");
  });
});
