import { describe, expect, it } from "vitest";
import ar from "@/locales/ar.json";
import de from "@/locales/de.json";
import en from "@/locales/en.json";
import es from "@/locales/es.json";
import fr from "@/locales/fr.json";
import tr from "@/locales/tr.json";
import { AUDIT_AREAS, KNOWN_ACTIONS, actionLabelKey, actorLine, auditQuery, dayStart, emptyFilter, isFiltered, prettyDetail, targetLine, type AuditFilter } from "../lib";

type Tree = { [key: string]: string | Tree };
const lookup = (tree: Tree, key: string): string | undefined => {
  let node: string | Tree | undefined = tree;
  for (const part of key.split(".")) node = typeof node === "object" ? node[part] : undefined;
  return typeof node === "string" ? node : undefined;
};
const filter = (extra: Partial<AuditFilter> = {}): AuditFilter => ({ ...emptyFilter(), ...extra });

describe("auditQuery", () => {
  it("sends nothing for an empty filter", () => {
    expect(auditQuery(emptyFilter())).toEqual({});
    expect(isFiltered(emptyFilter())).toBe(false);
  });

  it("sends an area as <area>.* and several areas as several values", () => {
    expect(auditQuery(filter({ areas: ["api_key"] }))).toEqual({ action: "api_key.*" });
    expect(auditQuery(filter({ areas: ["api_key", "member"] }))).toEqual({ action: "api_key.*,member.*" });
  });

  it("trims the actor and leaves a blank one out", () => {
    expect(auditQuery(filter({ actor: "  ada@example.com " }))).toEqual({ actor: "ada@example.com" });
    expect(auditQuery(filter({ actor: "   " }))).toEqual({});
  });

  it("sends the first day of the range as its start and the last day as the start of the day after it (to is exclusive)", () => {
    const q = auditQuery(filter({ range: { from: "2026-10-21", to: "2026-10-31" } }));
    expect(q.from).toBe(new Date(2026, 9, 21).toISOString());
    expect(q.to).toBe(new Date(2026, 10, 1).toISOString());
    // one day: [start of the day, start of the next)
    const one = auditQuery(filter({ range: { from: "2026-12-31", to: "2026-12-31" } }));
    expect(one.from).toBe(new Date(2026, 11, 31).toISOString());
    expect(one.to).toBe(new Date(2027, 0, 1).toISOString());
  });

  it("sends only the end that is set", () => {
    expect(auditQuery(filter({ range: { from: "2026-10-21", to: "" } }))).toEqual({ from: new Date(2026, 9, 21).toISOString() });
    expect(auditQuery(filter({ range: { from: "", to: "2026-10-21" } }))).toEqual({ to: new Date(2026, 9, 22).toISOString() });
  });

  it("carries the operator's tenant, trimmed", () => {
    expect(auditQuery(filter({ tenant: " 0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e " }))).toEqual({ tenant_id: "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e" });
  });

  it("says it is filtered as soon as any part is set", () => {
    for (const f of [filter({ areas: ["tenant"] }), filter({ actor: "x" }), filter({ tenant: "x" }), filter({ range: { from: "2026-10-21", to: "" } }), filter({ range: { from: "", to: "2026-10-21" } })]) expect(isFiltered(f)).toBe(true);
  });
});

describe("dayStart", () => {
  it("is an RFC 3339 instant, and nothing for text that is not a day", () => {
    expect(dayStart("2026-10-21")).toBe(new Date(2026, 9, 21).toISOString());
    expect(dayStart("2026-10-21", 1)).toBe(new Date(2026, 9, 22).toISOString());
    for (const bad of ["", "21/10/2026", "2026-10", "tomorrow"]) expect(dayStart(bad), bad).toBe("");
  });
});

describe("the label of an action", () => {
  it("is a translated label for a known action and the action itself for any other", () => {
    expect(actionLabelKey("api_key.created")).toBe("audit.actions.api_key.created");
    expect(actionLabelKey("something.new")).toBeNull();
    expect(actionLabelKey("api_key.*")).toBeNull();
  });

  it("every known action belongs to an area of the filter, and every area and action has its words in all six languages", () => {
    for (const a of KNOWN_ACTIONS) expect(AUDIT_AREAS as readonly string[], a).toContain(a.split(".")[0]);
    for (const [name, tree] of [["en", en], ["tr", tr], ["de", de], ["fr", fr], ["es", es], ["ar", ar]] as const) {
      for (const a of KNOWN_ACTIONS) expect(lookup(tree as Tree, `audit.actions.${a}`), `${name}: ${a}`).toBeTruthy();
      for (const area of AUDIT_AREAS) expect(lookup(tree as Tree, `audit.areas.${area}`), `${name}: area ${area}`).toBeTruthy();
    }
  });
});

describe("prettyDetail", () => {
  it("prints a JSON object indented", () => {
    expect(prettyDetail('{"role":"admin","keys":[1,2]}')).toBe('{\n  "role": "admin",\n  "keys": [\n    1,\n    2\n  ]\n}');
  });
  it("shows text that is not JSON as it came, and nothing for no detail", () => {
    expect(prettyDetail("not json {")).toBe("not json {");
    for (const none of ["", "  ", "{}", "null"]) expect(prettyDetail(none), JSON.stringify(none)).toBe("");
  });
  it("does not run or reshape anything in it", () => {
    expect(prettyDetail('{"summary":"<img src=x onerror=alert(1)>"}')).toContain("<img src=x onerror=alert(1)>");
  });
});

describe("the lines of a row", () => {
  it("names the actor by e-mail address, else by id, and the target by kind and id", () => {
    expect(actorLine({ actor_email: "ada@example.com", actor_id: "u1" })).toBe("ada@example.com");
    expect(actorLine({ actor_email: "", actor_id: "key_1" })).toBe("key_1");
    expect(targetLine({ target_type: "api_key", target_id: "k1" })).toBe("api_key · k1");
    expect(targetLine({ target_type: "", target_id: "" })).toBe("");
  });
});
