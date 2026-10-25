import { describe, expect, it } from "vitest";
import { latestOnly } from "./latest";

/** A promise a test settles by hand, to put two calls in the order that races. */
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

describe("latestOnly", () => {
  it("drops the result of a slow earlier call that finishes after a newer one", async () => {
    const run = latestOnly();
    const applied: string[] = [];
    const slow = deferred<string>();
    const fast = deferred<string>();

    const first = run(() => slow.promise, (v) => applied.push(v)); // TR: slow to load
    const second = run(() => fast.promise, (v) => applied.push(v)); // DE: chosen right after
    fast.resolve("de");
    await second;
    slow.resolve("tr"); // the older one lands last
    await first;

    expect(applied).toEqual(["de"]);
  });

  it("applies every call that is not overtaken", async () => {
    const run = latestOnly();
    const applied: number[] = [];
    await run(async () => 1, (v) => applied.push(v));
    await run(async () => 2, (v) => applied.push(v));
    expect(applied).toEqual([1, 2]);
  });
});
