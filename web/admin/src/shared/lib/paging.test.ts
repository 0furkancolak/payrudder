import { describe, expect, it } from "vitest";
import { matchesSearch, pageRange, paginate } from "./paging";

describe("pageRange", () => {
  it("lists every page when they fit", () => expect(pageRange(2, 5)).toEqual([1, 2, 3, 4, 5]));
  it("folds the far end", () => expect(pageRange(1, 20)).toEqual([1, 2, 3, 4, 5, "ellipsis", 20]));
  it("folds the near end", () => expect(pageRange(20, 20)).toEqual([1, "ellipsis", 16, 17, 18, 19, 20]));
  it("folds both ends in the middle", () => expect(pageRange(10, 20)).toEqual([1, "ellipsis", 9, 10, 11, "ellipsis", 20]));
});

describe("paginate", () => {
  const rows = Array.from({ length: 25 }, (_, i) => i);
  it("slices a page", () => expect(paginate(rows, 2, 10)).toMatchObject({ items: rows.slice(10, 20), pages: 3, page: 2 }));
  it("clamps a page past the end", () => expect(paginate(rows, 9, 10).page).toBe(3));
  it("has one page when empty", () => expect(paginate([], 1, 10)).toEqual({ items: [], pages: 1, page: 1 }));
});

describe("matchesSearch", () => {
  it("needs every word", () => {
    expect(matchesSearch(["Acme Ltd", "a@x.io"], "acme x.io")).toBe(true);
    expect(matchesSearch(["Acme Ltd"], "acme zzz")).toBe(false);
  });
  it("matches everything for an empty text", () => expect(matchesSearch(["a"], "  ")).toBe(true));
});
