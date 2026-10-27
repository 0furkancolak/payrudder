import { describe, expect, it } from "vitest";
import { BRANDS, CONNECTABLE, GROUPS, IN_GROUP, WITHOUT_LOGO, type IntegrationId } from "./brands";
import { builtInMailState, eInvoiceState, ownMailState, stateTone } from "./status";

describe("e-invoice state", () => {
  it("is connected only for the integrator that is stored", () => {
    expect(eInvoiceState({ provider: "nilvera", mode: "production" }, "nilvera")).toEqual({ state: "working", mode: "production" });
    expect(eInvoiceState({ provider: "nilvera", mode: "production" }, "webhook").state).toBe("none");
    expect(eInvoiceState(null, "nilvera").state).toBe("none");
    expect(eInvoiceState(undefined, "webhook").state).toBe("none");
  });
});

describe("e-mail state", () => {
  it("the own provider is working until a check found a problem", () => {
    expect(ownMailState(undefined)).toBe("none");
    expect(ownMailState({ connected: false })).toBe("none");
    expect(ownMailState({ connected: true, integration: {} })).toBe("working");
    expect(ownMailState({ connected: true, integration: { check_problem: "refused" } })).toBe("problem");
  });
  it("the built-in sender is always on, but not used while an own provider is connected", () => {
    expect(builtInMailState(undefined)).toBe("always");
    expect(builtInMailState({ connected: false })).toBe("always");
    expect(builtInMailState({ connected: true })).toBe("unused");
  });
  it("a problem is never shown in the colour of success", () => {
    expect(stateTone("problem")).toBe("danger");
    expect(stateTone("working")).toBe("success");
    expect(stateTone("none")).toBe("neutral");
  });
});

describe("the catalogue of integrations", () => {
  it("lists every integration in exactly one group, of its own group", () => {
    const listed = GROUPS.flatMap((g) => IN_GROUP[g]);
    expect(new Set(listed).size).toBe(listed.length);
    expect(listed.sort()).toEqual((Object.keys(BRANDS) as IntegrationId[]).sort());
    for (const g of GROUPS) for (const id of IN_GROUP[g]) expect(BRANDS[id].group).toBe(g);
  });
  it("the groups are payments, e-invoice and e-mail", () => {
    expect(GROUPS).toEqual(["payments", "einvoice", "email"]);
  });
  it("every brand shows a licensed logo, a general icon or only its name: never a drawn lookalike", () => {
    for (const id of Object.keys(BRANDS) as IntegrationId[]) {
      const b = BRANDS[id];
      expect(Boolean(b.logo) || Boolean(b.icon) || Boolean(b.name), id).toBe(true);
      // A company (it has a name) never has an icon in place of its logo; only a generic method does.
      if (b.name) expect(b.icon, id).toBeUndefined();
      if (b.logo) {
        expect(b.logo.light.startsWith("/brand/providers/"), id).toBe(true);
        expect(b.logo.dark.startsWith("/brand/providers/"), id).toBe(true);
      }
    }
  });
  it("every provider with a name has a licensed mark", () => {
    expect([...WITHOUT_LOGO].sort()).toEqual([]);
  });
  it("only the real services can be connected", () => {
    for (const id of CONNECTABLE) expect(["simulator", "einvoiceSimulator", "payrudderMail"]).not.toContain(id);
  });
});
