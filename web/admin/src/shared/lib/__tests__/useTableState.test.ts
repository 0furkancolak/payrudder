import { describe, expect, it } from "vitest";
import { nextTick, ref } from "vue";
import { parseValues, useTableState, writeValues } from "../useTableState";

const rows = ref(Array.from({ length: 25 }, (_, i) => ({ id: i, name: i % 2 ? `odd ${i}` : `even ${i}` })));
const make = () => useTableState(rows, { searchFields: (r) => [r.name], filters: { parity: (r, v) => (r.id % 2 === 0) === (v === "even") }, size: 10 });

describe("useTableState detail", () => {
  it("opens a row of the page shown as an absolute index", async () => {
    const t = make();
    t.page.value = 2;
    t.open(3);
    expect(t.detail.value).toBe(13);
    expect(t.row.value?.id).toBe(13);
    expect(t.isOpen(3)).toBe(true);
    expect(t.isOpen(2)).toBe(false);
  });

  it("moves the table to the page of the row the sheet shows", async () => {
    const t = make();
    t.open(9);
    t.detail.value = 10;
    await nextTick();
    expect(t.page.value).toBe(2);
    expect(t.items.value[0]?.id).toBe(10);
    t.detail.value = 24;
    await nextTick();
    expect(t.page.value).toBe(3);
    t.detail.value = 3;
    await nextTick();
    expect(t.page.value).toBe(1);
  });

  it("walks only the filtered rows", async () => {
    const t = make();
    t.filters.value = { parity: ["even"] };
    await nextTick();
    expect(t.total.value).toBe(13);
    t.detail.value = 12;
    await nextTick();
    expect(t.row.value?.id).toBe(24);
    expect(t.page.value).toBe(2);
  });

  it("opens the first row that matches, across pages, and says when none does", async () => {
    const t = make();
    expect(t.openWhere((r) => r.id === 21)).toBe(true);
    await nextTick();
    expect(t.page.value).toBe(3);
    expect(t.row.value?.id).toBe(21);
    expect(t.openWhere((r) => r.id === 99)).toBe(false);
    expect(t.row.value?.id).toBe(21);
  });

  it("closes when the list shrinks under it", async () => {
    const own = ref([{ id: 1 }, { id: 2 }, { id: 3 }]);
    const t = useTableState(own, { searchFields: (r) => [r.id] });
    t.detail.value = 2;
    own.value = own.value.slice(0, 1);
    await nextTick();
    expect(t.detail.value).toBeNull();
    expect(t.row.value).toBeUndefined();
  });
});

describe("useTableState filters with several values", () => {
  it("passes a row that matches any of the values, and every row when there are none", async () => {
    const t = make();
    t.filters.value = { parity: ["even", "odd"] };
    await nextTick();
    expect(t.total.value).toBe(25);
    t.filters.value = { parity: ["even"] };
    await nextTick();
    expect(t.total.value).toBe(13);
    t.filters.value = { parity: [] };
    await nextTick();
    expect(t.total.value).toBe(25);
    expect(t.active.value).toBe(false);
  });

  it("is active while any value is chosen, and clear() removes them all", () => {
    const t = make();
    t.filters.value = { parity: ["odd"] };
    expect(t.active.value).toBe(true);
    t.clear();
    expect(t.filters.value).toEqual({});
  });
});

describe("filter values in the address", () => {
  it("reads and writes comma separated values, escaping a comma inside one", () => {
    expect(parseValues("open,paid")).toEqual(["open", "paid"]);
    expect(parseValues(undefined)).toEqual([]);
    expect(parseValues("")).toEqual([]);
    expect(parseValues(5)).toEqual(["5"]);
    expect(writeValues(["open", "paid"])).toBe("open,paid");
    expect(writeValues([])).toBeUndefined();
    expect(parseValues(writeValues(["a,b", "100%", "c"]))).toEqual(["a,b", "100%", "c"]);
  });
});

describe("useTableState select all", () => {
  const make2 = () => useTableState(rows, { searchFields: (r) => [r.name], filters: { parity: (r, v) => (r.id % 2 === 0) === (v === "even") }, size: 10, selectId: (r) => String(r.id) });

  it("offers to take every matching row once the page is ticked and more rows match", () => {
    const t = make2();
    expect(t.canSelectAll.value).toBe(false);
    t.togglePage();
    expect(t.selectedRows.value).toHaveLength(10);
    expect(t.canSelectAll.value).toBe(true);
    t.selectAll();
    expect(t.selectedRows.value).toHaveLength(25);
    expect(t.allMatching.value).toBe(true);
    expect(t.canSelectAll.value).toBe(false);
  });

  it("takes only the rows the search and filters show, and drops to those when a filter narrows the selection", async () => {
    const t = make2();
    t.filters.value = { parity: ["even"] };
    await nextTick();
    t.togglePage();
    t.selectAll();
    expect(t.selectedRows.value.map((r) => r.id)).toEqual([0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24]);
    t.filters.value = {};
    await nextTick();
    // The odd rows were never ticked: the broader filter does not select them.
    expect(t.selectedRows.value).toHaveLength(13);
    expect(t.allMatching.value).toBe(false);
  });

  it("is not offered when everything matching fits on the page, or on a list that cannot be ticked", () => {
    const own = ref([{ id: 1 }, { id: 2 }]);
    const t = useTableState(own, { searchFields: (r) => [r.id], selectId: (r) => String(r.id) });
    t.togglePage();
    expect(t.canSelectAll.value).toBe(false);
    const plain = useTableState(rows, { searchFields: (r) => [r.name], size: 10 });
    plain.selectAll();
    expect(plain.selectedRows.value).toHaveLength(0);
  });
});
