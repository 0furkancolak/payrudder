import { describe, expect, it } from "vitest";
import ar from "./ar.json";
import de from "./de.json";
import en from "./en.json";
import es from "./es.json";
import fr from "./fr.json";
import tr from "./tr.json";

type Tree = { [key: string]: string | Tree };

function flatten(tree: Tree, prefix = ""): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(tree)) {
    if (typeof v === "string") out[prefix + k] = v;
    else Object.assign(out, flatten(v, `${prefix}${k}.`));
  }
  return out;
}

const placeholders = (s: string): string[] => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1] as string).sort();
const base = flatten(en as Tree);

describe.each([
  ["tr", tr],
  ["de", de],
  ["es", es],
  ["fr", fr],
  ["ar", ar],
])("locale %s", (_name, file) => {
  const messages = flatten(file as Tree);

  it("has exactly the keys of English", () => {
    expect(Object.keys(messages).sort()).toEqual(Object.keys(base).sort());
  });

  it("uses the same placeholders as English in every message", () => {
    for (const [key, text] of Object.entries(base)) {
      expect(placeholders(messages[key] ?? ""), key).toEqual(placeholders(text));
    }
  });

  it("has no empty message", () => {
    for (const [key, text] of Object.entries(messages)) expect(text.trim(), key).not.toBe("");
  });
});

it("declares a reading direction for every locale", () => {
  for (const file of [en, tr, de, es, fr, ar]) expect(["ltr", "rtl"]).toContain(file.meta.dir);
});
