import { describe, expect, it, vi } from "vitest";
import { createWriter, mergePatches, patchIsEmpty, type OnboardingPatch } from "./onboarding";

describe("joining two changes", () => {
  it("lets the later wizard field win and keeps the others", () => {
    expect(mergePatches({ wizard: { step: "meter", app: "a" } }, { wizard: { step: "package" } })).toEqual({ wizard: { step: "package", app: "a" } });
  });

  it("adds tours together once", () => {
    expect(mergePatches({ seen_tours: ["a", "b"] }, { seen_tours: ["b", "c"] })).toEqual({ seen_tours: ["a", "b", "c"] });
  });

  it("does what the two would do in order: forgetting then seeing is seen, seeing then forgetting is forgotten", () => {
    expect(mergePatches({ forget_tours: ["x"] }, { seen_tours: ["x"] })).toEqual({ seen_tours: ["x"] });
    expect(mergePatches({ seen_tours: ["x", "y"] }, { forget_tours: ["x"] })).toEqual({ seen_tours: ["y"], forget_tours: ["x"] });
  });

  it("knows an empty change", () => {
    expect(patchIsEmpty({})).toBe(true);
    expect(patchIsEmpty({ wizard: {}, seen_tours: [] })).toBe(true);
    expect(patchIsEmpty({ forget_tours: ["a"] })).toBe(false);
  });
});

describe("writing through", () => {
  const sent: OnboardingPatch[] = [];
  const recorder = (fail = 0) => {
    let failures = fail;
    sent.length = 0;
    return vi.fn(async (p: OnboardingPatch) => {
      if (failures > 0) {
        failures--;
        throw new Error("offline");
      }
      sent.push(p);
    });
  };

  it("sends one request at a time and joins what was changed while one was on its way", async () => {
    let release: () => void = () => undefined;
    const send = vi.fn(async (p: OnboardingPatch) => {
      sent.push(p);
      if (sent.length === 1) await new Promise<void>((r) => (release = r));
    });
    sent.length = 0;
    const w = createWriter(send, () => "t1", 0);
    w.push({ seen_tours: ["a"] }, "t1");
    await vi.waitFor(() => expect(send).toHaveBeenCalledTimes(1));
    w.push({ seen_tours: ["b"] }, "t1");
    w.push({ wizard: { step: "app" } }, "t1");
    expect(sent).toEqual([{ seen_tours: ["a"] }]);
    release();
    await w.flush();
    expect(sent).toEqual([{ seen_tours: ["a"] }, { seen_tours: ["b"], wizard: { step: "app" } }]);
  });

  it("keeps a change that failed and sends it with the next one", async () => {
    const send = recorder(1);
    const w = createWriter(send, () => "t1", 0);
    w.push({ seen_tours: ["a"] }, "t1");
    await w.flush();
    expect(sent).toEqual([]);
    w.push({ seen_tours: ["b"] }, "t1");
    await w.flush();
    expect(sent).toEqual([{ seen_tours: ["a", "b"] }]);
  });

  it("tries a failed change again by itself", async () => {
    vi.useFakeTimers();
    try {
      const w = createWriter(recorder(1), () => "t1", 1000);
      w.push({ seen_tours: ["a"] }, "t1");
      await w.flush();
      expect(sent).toEqual([]);
      await vi.advanceTimersByTimeAsync(1000);
      await w.flush();
      expect(sent).toEqual([{ seen_tours: ["a"] }]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("drops a change made under a session that is over", async () => {
    let now = "t1";
    const send = recorder();
    const w = createWriter(send, () => now, 0);
    now = "t2";
    w.push({ seen_tours: ["a"] }, "t1");
    await w.flush();
    expect(send).not.toHaveBeenCalled();
    w.push({ seen_tours: ["b"] }, "t2");
    await w.flush();
    expect(sent).toEqual([{ seen_tours: ["b"] }]);
  });
});
