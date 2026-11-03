import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const src = fileURLToPath(new URL("../..", import.meta.url));
function vueFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? vueFiles(`${dir}/${e.name}`) : e.name.endsWith(".vue") ? [`${dir}/${e.name}`] : []));
}

describe("rows that open a sheet", () => {
  // A row that opens its sheet from a click on the row only can be neither tabbed to nor opened with Enter or Space. Every table gives the keyboard the same way in:
  // a "View" button in the row's last cell (RowActions), or a row that takes the focus itself.
  it("can be reached by keyboard: every page with a clickable row has a RowActions button or a focusable row", () => {
    const clickOnly = vueFiles(src)
      .filter((f) => /<TableRow[^>]*@click/.test(readFileSync(f, "utf8")))
      .filter((f) => !/RowActions|tabindex/.test(readFileSync(f, "utf8")))
      .map((f) => f.slice(src.length));
    expect(clickOnly).toEqual([]);
  });
});
