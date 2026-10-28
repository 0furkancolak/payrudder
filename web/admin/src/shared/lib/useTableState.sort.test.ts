import { describe, expect, it } from "vitest";
import { sorted, type SortDef } from "./useTableState";

interface Row {
  name: string | null;
  n: number | null;
}
const rows: Row[] = [{ name: "b", n: 2 }, { name: null, n: null }, { name: "a10", n: 10 }, { name: "a2", n: 1 }];
const def = (by: SortDef<Row>["by"], dir: "asc" | "desc"): SortDef<Row> => ({ key: "k", label: "k", by, dir });

describe("sorted", () => {
  it("keeps the arrival order without a choice, and never changes the input", () => {
    expect(sorted(rows, undefined)).toEqual(rows);
    sorted(rows, def((r) => r.n, "asc"));
    expect(rows[0]?.name).toBe("b");
  });
  it("orders numbers numerically and text by natural collation, empty values last either way", () => {
    expect(sorted(rows, def((r) => r.n, "asc")).map((r) => r.n)).toEqual([1, 2, 10, null]);
    expect(sorted(rows, def((r) => r.n, "desc")).map((r) => r.n)).toEqual([10, 2, 1, null]);
    expect(sorted(rows, def((r) => r.name, "asc")).map((r) => r.name)).toEqual(["a2", "a10", "b", null]);
    expect(sorted(rows, def((r) => r.name, "desc")).map((r) => r.name)).toEqual(["b", "a10", "a2", null]);
  });
  it("is stable for equal values", () => {
    const same = [{ name: "x", n: 1 }, { name: "y", n: 1 }, { name: "z", n: 1 }];
    expect(sorted(same, def((r) => r.n, "desc")).map((r) => r.name)).toEqual(["x", "y", "z"]);
  });
});
