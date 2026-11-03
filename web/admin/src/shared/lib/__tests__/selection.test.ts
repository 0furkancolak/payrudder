import { describe, expect, it } from "vitest";
import { ref } from "vue";
import { useSelection, useServerPageSelection } from "../selection";

const rows = (...ids: string[]) => ids.map((id) => ({ id }));

describe("useSelection", () => {
  it("acts only on the ticked rows that are still shown, and keeps ticks by id across a refetch", () => {
    const shown = ref(rows("a", "b", "c"));
    const s = useSelection(shown, shown, (r) => r.id);
    s.tick({ id: "a" }, true);
    s.tick({ id: "c" }, true);
    expect(s.selectedRows.value.map((r) => r.id)).toEqual(["a", "c"]);
    // A refetch brings new objects with the same ids: the ticks stay. A row that is no longer shown is not acted on.
    shown.value = rows("a", "b");
    expect(s.selectedRows.value.map((r) => r.id)).toEqual(["a"]);
    shown.value = rows("c", "a");
    expect(s.selectedRows.value.map((r) => r.id)).toEqual(["c", "a"]);
  });

  it("ticks and unticks the page with the header, and offers the rest only when more rows match than the page holds", () => {
    const matching = ref(rows("a", "b", "c", "d"));
    const page = ref(rows("a", "b"));
    const s = useSelection(matching, page, (r) => r.id);
    s.togglePage();
    expect(s.allOnPage.value).toBe(true);
    expect(s.canSelectAll.value).toBe(true);
    s.selectAll();
    expect(s.selectedRows.value).toHaveLength(4);
    expect(s.canSelectAll.value).toBe(false);
    s.clearSelection();
    expect(s.someOnPage.value).toBe(false);
  });

  it("ticks nothing for a row with no id", () => {
    const s = useSelection(ref([{}]), ref([{}]), () => undefined);
    s.togglePage();
    expect(s.selectedRows.value).toHaveLength(0);
  });
});

describe("useServerPageSelection", () => {
  it("holds the page the server gave: its size is the page's own length, so a bulk action on it never needs the 'more than a page' question", () => {
    const page = ref(rows("a", "b", "c"));
    const s = useServerPageSelection(page, (r) => r.id);
    expect(s.size.value).toBe(3);
    expect(s.total.value).toBe(3);
    s.togglePage();
    expect(s.selectedRows.value).toHaveLength(3);
    expect(s.canSelectAll.value).toBe(false);
    // The next page replaces the rows: the first page's ticks no longer count.
    page.value = rows("x", "y");
    expect(s.selectedRows.value).toHaveLength(0);
    expect(s.size.value).toBe(2);
  });
});
