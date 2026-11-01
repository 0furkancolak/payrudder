import { describe, expect, it } from "vitest";
import { ref } from "vue";
import { bulkQuestion, restOfPages, runBulk, selectAllResults } from "./bulk";

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

describe("restOfPages", () => {
  it("follows the page tokens to the end and returns every row after the first page", async () => {
    const pages: Record<string, { rows: number[]; next: string }> = { t1: { rows: [3, 4], next: "t2" }, t2: { rows: [5], next: "" } };
    const asked: string[] = [];
    const rows = await restOfPages("t1", async (token) => (asked.push(token), pages[token] as { rows: number[]; next: string }));
    expect(rows).toEqual([3, 4, 5]);
    expect(asked).toEqual(["t1", "t2"]);
  });

  it("asks for nothing when the first page was the last", async () => {
    expect(await restOfPages("", () => Promise.reject(new Error("no")))).toEqual([]);
  });

  it("stops with an error on a token that comes back, and on a list that never ends", async () => {
    await expect(restOfPages("a", async () => ({ rows: [1], next: "a" }))).rejects.toThrow();
    let n = 0;
    await expect(restOfPages("0", async () => ({ rows: [1], next: String(++n) }), 5)).rejects.toThrow();
  });
});

describe("selectAllResults", () => {
  it("ticks at once when the list holds everything", async () => {
    const calls: string[] = [];
    await selectAllResults(() => calls.push("select"), { has: () => false, load: () => Promise.reject(new Error("no")) });
    await selectAllResults(() => calls.push("select"));
    expect(calls).toEqual(["select", "select"]);
  });

  it("loads the rest first and ticks only once the list shows it", async () => {
    const more = ref(true);
    const calls: string[] = [];
    await selectAllResults(() => calls.push(`select (more: ${more.value})`), {
      has: () => more.value,
      load: async () => {
        calls.push("load");
        setTimeout(() => (more.value = false), 5);
      },
    });
    expect(calls).toEqual(["load", "select (more: false)"]);
  });

  it("does not tick when the rest cannot be loaded", async () => {
    const calls: string[] = [];
    await expect(selectAllResults(() => calls.push("select"), { has: () => true, load: () => Promise.reject(new Error("down")) })).rejects.toThrow("down");
    expect(calls).toEqual([]);
  });
});

describe("bulkQuestion", () => {
  const ours = { title: "Apply to 12 rows?", count: "This acts on all 12 selected rows." };
  it("leaves an action on one page alone: its own question, or none", () => {
    expect(bulkQuestion(undefined, 10, 10, ours)).toBeUndefined();
    const own = { body: "Revoke?" };
    expect(bulkQuestion(own, 3, 10, ours)).toBe(own);
  });

  it("always asks, with the count, about more rows than a page holds, and not red when the action is not destructive", () => {
    expect(bulkQuestion(undefined, 12, 10, ours)).toEqual({ title: ours.title, body: ours.count, destructive: false });
  });

  it("adds the count to the question an action asks anyway, keeping its title and its red button", () => {
    const own = { title: "Suspend?", body: "Their keys stop working.", confirmLabel: "Suspend" };
    expect(bulkQuestion(own, 12, 10, ours)).toEqual({ title: "Suspend?", body: "Their keys stop working. This acts on all 12 selected rows.", confirmLabel: "Suspend", destructive: undefined });
  });
});
