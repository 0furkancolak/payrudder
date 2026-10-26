import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { checklist, doneCount, type Facts } from "./checklist";
import { finishOnboarding, hideChecklist, loadProgress, patchProgress, progress, resumeOnboarding, skipOnboarding, slug, stepBy, wizardOpen } from "./state";
import { METER_TEMPLATES, templateByCode } from "./templates";

let store: Map<string, string>;
beforeEach(() => {
  store = new Map();
  vi.stubGlobal("window", { localStorage: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v), removeItem: (k: string) => void store.delete(k) } });
  loadProgress("u1");
});
afterEach(() => vi.unstubAllGlobals());

describe("who sees the wizard", () => {
  it("a new sign-up (no app, never started) does; somebody with an app does not", () => {
    expect(wizardOpen(null, { appCount: 0, canCreate: true })).toBe(true);
    expect(wizardOpen(null, { appCount: 2, canCreate: true })).toBe(false);
  });

  it("waits until the apps are known, and never opens for somebody who cannot create anything", () => {
    expect(wizardOpen(null, { appCount: undefined, canCreate: true })).toBe(false);
    expect(wizardOpen(null, { appCount: 0, canCreate: false })).toBe(false);
  });

  it("keeps opening while it is half done, even once the first app exists, and stays shut after a skip or the end", () => {
    expect(wizardOpen({ status: "active", step: "meter" }, { appCount: 1, canCreate: true })).toBe(true);
    expect(wizardOpen({ status: "skipped", step: "app" }, { appCount: 0, canCreate: true })).toBe(false);
    expect(wizardOpen({ status: "done", step: "done" }, { appCount: 0, canCreate: true })).toBe(false);
  });
});

describe("progress is kept per person", () => {
  it("is resumable: a step and what was made survive a reload", () => {
    patchProgress({ step: "package", app: "app-1", meter: "api_calls" });
    expect(JSON.parse(store.get("payrudder.onboarding.u1") ?? "{}")).toMatchObject({ status: "active", step: "package", app: "app-1", meter: "api_calls" });
    progress.value = null;
    loadProgress("u1");
    expect(progress.value).toMatchObject({ step: "package", app: "app-1" });
  });

  it("is not shared with another person on the same browser", () => {
    patchProgress({ step: "meter" });
    loadProgress("u2");
    expect(progress.value).toBeNull();
    loadProgress("u1");
    expect(progress.value?.step).toBe("meter");
  });

  it("can be skipped, resumed where it stopped, finished, and its checklist hidden", () => {
    patchProgress({ step: "meter", app: "a" });
    skipOnboarding();
    expect(progress.value).toMatchObject({ status: "skipped", step: "meter", app: "a" });
    resumeOnboarding();
    expect(progress.value).toMatchObject({ status: "active", step: "meter" });
    finishOnboarding();
    expect(progress.value).toMatchObject({ status: "done", step: "done" });
    hideChecklist();
    expect(progress.value?.hidden).toBe(true);
  });

  it("ignores what it cannot read", () => {
    store.set("payrudder.onboarding.u3", "{not json");
    loadProgress("u3");
    expect(progress.value).toBeNull();
    store.set("payrudder.onboarding.u3", JSON.stringify({ status: "weird" }));
    loadProgress("u3");
    expect(progress.value).toBeNull();
    store.set("payrudder.onboarding.u3", JSON.stringify({ status: "active", step: "nowhere" }));
    loadProgress("u3");
    expect(progress.value?.step).toBe("welcome");
  });

  it("moves between steps inside the list", () => {
    expect(stepBy("welcome", -1)).toBe("welcome");
    expect(stepBy("organisation", 1)).toBe("app");
    expect(stepBy("done", 1)).toBe("done");
  });
});

describe("the checklist reads the data", () => {
  const none: Facts = { apps: 0, meters: 0, packages: 0, event: false, keys: 0, live: false };

  it("ticks what exists, whoever made it", () => {
    expect(doneCount(checklist(none))).toBe(0);
    const rows = checklist({ ...none, apps: 1, meters: 2, packages: 1 });
    expect(rows.filter((r) => r.done).map((r) => r.key)).toEqual(["app", "meter", "package"]);
    expect(doneCount(checklist({ apps: 1, meters: 1, packages: 1, event: true, keys: 1, live: true }))).toBe(6);
  });
});

describe("package codes", () => {
  it("are what the API accepts", () => {
    expect(slug("Starter")).toBe("starter");
    expect(slug("Başlangıç Paketi 2")).toBe("baslangic-paketi-2");
    expect(slug("2024 plan")).toBe("p-2024-plan");
    expect(slug("***")).toBe("starter");
    expect(slug("a".repeat(100)).length).toBe(63);
    for (const name of ["Pro", "çok güzel", "x_y z", "1"]) expect(slug(name)).toMatch(/^[a-z][a-z0-9_-]{0,62}$/);
  });
});

describe("meter templates", () => {
  it("are valid meters: a sum or a maximum names the property it reads, codes are the API's", () => {
    for (const m of METER_TEMPLATES) {
      expect(m.meter.code).toMatch(/^[a-z][a-z0-9_]{0,62}$/);
      expect(m.meter.unit_label.length).toBeGreaterThan(0);
      if (m.meter.aggregation !== "AGGREGATION_COUNT") expect(m.meter.value_property).toBeTruthy();
      expect(Number(m.sample)).toBeGreaterThan(0);
      expect(templateByCode(m.meter.code)).toBe(m);
    }
    expect(new Set(METER_TEMPLATES.map((m) => m.meter.code)).size).toBe(METER_TEMPLATES.length);
  });
});
