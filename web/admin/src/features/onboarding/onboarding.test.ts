import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { checklist, doneCount, type Facts } from "./checklist";
import { finishOnboarding, hideChecklist, loadProgress, patchProgress, progress, progressReady, resumeOnboarding, setProgressStore, skipOnboarding, stepBy, wizardOpen, type Progress, type ProgressStore } from "./state";

let store: Map<string, string>;
beforeEach(() => {
  store = new Map();
  vi.stubGlobal("window", { localStorage: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v), removeItem: (k: string) => void store.delete(k) } });
  setProgressStore(null);
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
    expect(wizardOpen({ status: "active", step: "app" }, { appCount: 1, canCreate: true })).toBe(true);
    expect(wizardOpen({ status: "skipped", step: "app" }, { appCount: 0, canCreate: true })).toBe(false);
    expect(wizardOpen({ status: "done", step: "done" }, { appCount: 0, canCreate: true })).toBe(false);
  });
});

describe("progress is kept per person", () => {
  it("is resumable: a step and what was made survive a reload", () => {
    patchProgress({ step: "app", app: "app-1" });
    expect(JSON.parse(store.get("payrudder.onboarding.u1") ?? "{}")).toMatchObject({ status: "active", step: "app", app: "app-1" });
    progress.value = null;
    loadProgress("u1");
    expect(progress.value).toMatchObject({ step: "app", app: "app-1" });
  });

  it("is not shared with another person on the same browser", () => {
    patchProgress({ step: "app" });
    loadProgress("u2");
    expect(progress.value).toBeNull();
    loadProgress("u1");
    expect(progress.value?.step).toBe("app");
  });

  it("can be skipped, resumed where it stopped, finished, and its checklist hidden", () => {
    patchProgress({ step: "app", app: "a" });
    skipOnboarding();
    expect(progress.value).toMatchObject({ status: "skipped", step: "app", app: "a" });
    resumeOnboarding();
    expect(progress.value).toMatchObject({ status: "active", step: "app" });
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
    expect(stepBy("welcome", 1)).toBe("app");
    expect(stepBy("done", 1)).toBe("done");
  });
});

describe("the checklist reads the data", () => {
  const none: Facts = { apps: 0, meters: 0, packages: 0, keys: 0, live: false };

  it("ticks what exists, whoever made it", () => {
    expect(doneCount(checklist(none))).toBe(0);
    const rows = checklist({ ...none, apps: 1, meters: 2, packages: 1 });
    expect(rows.filter((r) => r.done).map((r) => r.key)).toEqual(["app", "meter", "package"]);
    expect(doneCount(checklist({ apps: 1, meters: 1, packages: 1, keys: 1, live: true }))).toBe(5);
  });
});

/** A server that keeps the wizard of a person, and answers when the test lets it. */
function fakeServer(initial: Partial<Progress> | null | "down") {
  let answer: () => void = () => undefined;
  const patches: Partial<Progress>[] = [];
  const server: ProgressStore = {
    load: () => new Promise((r) => (answer = () => r(initial === "down" ? null : { wizard: initial }))),
    patch: (c) => void patches.push(c),
  };
  return { server, patches, answer: () => answer() };
}
const settle = () => new Promise((r) => setTimeout(r, 0));

describe("progress kept on the server", () => {
  it("what the server has wins over this browser's copy, and the home waits for it", async () => {
    store.set("payrudder.onboarding.s1", JSON.stringify({ status: "active", step: "app" }));
    const f = fakeServer({ status: "done", step: "done", app: "a1" });
    setProgressStore(f.server);
    loadProgress("s1");
    expect(progressReady.value).toBe(false);
    f.answer();
    await settle();
    expect(progressReady.value).toBe(true);
    expect(progress.value).toMatchObject({ status: "done", step: "done", app: "a1" });
    // The browser keeps it too, for the next page load.
    expect(JSON.parse(store.get("payrudder.onboarding.s1") ?? "{}")).toMatchObject({ status: "done" });
    expect(f.patches).toEqual([]);
  });

  it("a second browser with nothing takes the server's progress", async () => {
    const f = fakeServer({ status: "skipped", step: "app", hidden: true });
    setProgressStore(f.server);
    loadProgress("s2");
    expect(progress.value).toBeNull();
    f.answer();
    await settle();
    expect(progress.value).toMatchObject({ status: "skipped", step: "app", hidden: true });
    expect(wizardOpen(progress.value, { appCount: 0, canCreate: true })).toBe(false);
  });

  it("sends up the progress only this browser has, and the fields the server lacks", async () => {
    store.set("payrudder.onboarding.s3", JSON.stringify({ status: "active", step: "app", app: "a", meter: "m" }));
    const f = fakeServer(null);
    setProgressStore(f.server);
    loadProgress("s3");
    f.answer();
    await settle();
    expect(f.patches).toEqual([{ status: "active", step: "app", app: "a", meter: "m" }]);

    store.set("payrudder.onboarding.s4", JSON.stringify({ status: "active", step: "app", app: "a", meter: "m", currency: "TRY" }));
    const g = fakeServer({ status: "active", step: "app", app: "a" });
    setProgressStore(g.server);
    loadProgress("s4");
    g.answer();
    await settle();
    expect(g.patches).toEqual([{ meter: "m", currency: "TRY" }]);
    expect(progress.value).toMatchObject({ step: "app", app: "a", meter: "m", currency: "TRY" });
  });

  it("goes on with this browser's copy when the server cannot say, and asks again the next time", async () => {
    store.set("payrudder.onboarding.s5", JSON.stringify({ status: "active", step: "app" }));
    const f = fakeServer("down");
    setProgressStore(f.server);
    loadProgress("s5");
    f.answer();
    await settle();
    expect(progressReady.value).toBe(true);
    expect(progress.value?.step).toBe("app");
    const g = fakeServer({ status: "done", step: "done" });
    setProgressStore(g.server);
    loadProgress("s5");
    expect(progressReady.value).toBe(false);
    g.answer();
    await settle();
    expect(progress.value?.status).toBe("done");
  });

  it("asks once per person: coming back to the home does not undo what was done since", async () => {
    const f = fakeServer({ status: "active", step: "app" });
    setProgressStore(f.server);
    loadProgress("s6");
    f.answer();
    await settle();
    patchProgress({ step: "app" });
    const g = fakeServer({ status: "active", step: "app" });
    setProgressStore(g.server);
    loadProgress("s6");
    expect(progressReady.value).toBe(true);
    expect(progress.value?.step).toBe("app");
  });

  it("a change made while the server is being asked for is newer than its answer", async () => {
    const f = fakeServer({ status: "active", step: "app", app: "a" });
    setProgressStore(f.server);
    loadProgress("s7");
    patchProgress({ step: "app" });
    f.answer();
    await settle();
    expect(progress.value).toMatchObject({ step: "app", app: "a" });
  });

  it("writes every change through, as the change and not the whole", () => {
    const f = fakeServer({ status: "active", step: "welcome" });
    setProgressStore(f.server);
    loadProgress("s8");
    patchProgress({ step: "app", currency: "USD" });
    skipOnboarding();
    hideChecklist();
    expect(f.patches).toEqual([{ step: "app", currency: "USD" }, { status: "skipped" }, { hidden: true }]);
  });
});
