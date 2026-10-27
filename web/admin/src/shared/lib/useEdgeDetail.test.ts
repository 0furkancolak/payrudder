import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { computed, effectScope, nextTick, ref, type EffectScope, type Ref } from "vue";
import { names } from "./sheets";
import { useEdgeDetail } from "./useEdgeDetail";

/** Two server pages of three rows each; "loading" a page empties the rows for a moment, as a query with a new token does. */
function setup() {
  const pageNo = ref(0);
  const loading = ref(false);
  const data = [["a", "b", "c"], ["d", "e", "f"]];
  const rows = computed(() => (loading.value ? [] : (data[pageNo.value] ?? [])));
  const go = async (to: number) => {
    loading.value = true;
    pageNo.value = to;
    await nextTick();
    loading.value = false;
  };
  const nav = useEdgeDetail(rows, { hasPrev: () => pageNo.value > 0, hasNext: () => pageNo.value < data.length - 1, prev: () => go(pageNo.value - 1), next: () => go(pageNo.value + 1) });
  return { nav, pageNo };
}

describe("useEdgeDetail", () => {
  it("offers the edges only where a page lies beyond", () => {
    const { nav } = setup();
    expect(nav.edge.value).toEqual({ prev: false, next: true });
  });

  it("lands on the first row of the next page once it has loaded", async () => {
    const { nav, pageNo } = setup();
    nav.index.value = 2;
    nav.onEdge("next");
    expect(nav.edge.value.next).toBe(false);
    await new Promise((r) => setTimeout(r));
    expect(pageNo.value).toBe(1);
    expect(nav.row.value).toBe("d");
    expect(nav.index.value).toBe(0);
  });

  it("lands on the last row of the previous page going back", async () => {
    const { nav, pageNo } = setup();
    pageNo.value = 1;
    nav.index.value = 0;
    nav.onEdge("prev");
    await new Promise((r) => setTimeout(r));
    expect(pageNo.value).toBe(0);
    expect(nav.row.value).toBe("c");
  });

  it("ignores an edge while no sheet is open", () => {
    const { nav, pageNo } = setup();
    nav.onEdge("next");
    expect(pageNo.value).toBe(0);
  });
});

describe("useEdgeDetail with the sheet in the address", () => {
  // The watchers of one test must not outlive it: they would answer the next test's address.
  let scope: EffectScope | undefined;
  afterEach(() => scope?.stop());
  function paged(named?: string, missing?: (id: string, pageNo: Ref<number>) => boolean) {
    names.value = named ? [named] : [];
    const pageNo = ref(0);
    const loading = ref(false);
    const data = [["a", "b", "c"], ["d", "e", "f"], ["g", "h", "i"]];
    const rows = computed(() => (loading.value ? [] : (data[pageNo.value] ?? [])));
    const go = async (to: number) => {
      loading.value = true;
      pageNo.value = to;
      await nextTick();
      loading.value = false;
    };
    scope = effectScope();
    const nav = scope.run(() =>
      useEdgeDetail(
        rows,
        { hasPrev: () => pageNo.value > 0, hasNext: () => pageNo.value < data.length - 1, prev: () => go(pageNo.value - 1), next: () => go(pageNo.value + 1) },
        { kind: "row", id: (r) => r, rewind: () => (pageNo.value = 0), ...(missing ? { missing: (id: string) => missing(id, pageNo) } : {}) },
      ),
    )!;
    return { nav, pageNo };
  }
  const settle = () => new Promise((r) => setTimeout(r, 10));

  beforeEach(() => {
    names.value = [];
  });

  it("writes the open row into the address, follows the arrows and the edge, and takes it off when closed", async () => {
    const { nav } = paged();
    nav.index.value = 2;
    expect(names.value).toEqual(["row:c"]);
    nav.onEdge("next");
    // The page is on its way: the sheet stays open and keeps its name.
    expect(nav.index.value).toBe(2);
    expect(names.value).toEqual(["row:c"]);
    await settle();
    expect(names.value).toEqual(["row:d"]);
    nav.index.value = null;
    expect(names.value).toEqual([]);
  });

  it("opens the row the address names when it is on the page loaded", () => {
    const { nav } = paged("row:b");
    expect(nav.row.value).toBe("b");
  });

  it("looks for the row on the pages after the first", async () => {
    const { nav, pageNo } = paged("row:h");
    await settle();
    expect(pageNo.value).toBe(2);
    expect(nav.row.value).toBe("h");
    expect(names.value).toEqual(["row:h"]);
  });

  it("takes a row that no page holds off the address and goes back to the first page", async () => {
    const { nav, pageNo } = paged("row:zz");
    await settle();
    expect(nav.index.value).toBeNull();
    expect(names.value).toEqual([]);
    expect(pageNo.value).toBe(0);
  });

  it("lets the page change what is listed first when the row is hidden", async () => {
    let calls = 0;
    const { nav } = paged("row:e", (_, pageNo) => {
      calls++;
      if (calls > 1) return false;
      pageNo.value = 1;
      return true;
    });
    await settle();
    expect(calls).toBe(1);
    expect(nav.row.value).toBe("e");
  });

  it("closes the sheet when the address stops naming the row", async () => {
    const { nav } = paged("row:a");
    expect(nav.row.value).toBe("a");
    names.value = [];
    await nextTick();
    expect(nav.index.value).toBeNull();
  });
});
