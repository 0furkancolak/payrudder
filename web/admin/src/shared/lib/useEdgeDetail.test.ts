import { describe, expect, it } from "vitest";
import { computed, nextTick, ref } from "vue";
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
