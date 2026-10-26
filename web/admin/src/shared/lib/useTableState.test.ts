import { describe, expect, it } from "vitest";
import { nextTick, ref } from "vue";
import { useTableState } from "./useTableState";

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
    t.filters.value = { parity: "even" };
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
