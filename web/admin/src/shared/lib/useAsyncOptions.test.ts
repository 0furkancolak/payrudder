import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useAsyncOptions } from "./useAsyncOptions";

/** A loader the test answers by hand: every call is kept with the function that answers it. */
function manual() {
  const calls: { text: string; signal: AbortSignal; resolve: (v: string[]) => void; reject: (e: unknown) => void }[] = [];
  const load = (text: string, signal: AbortSignal) => new Promise<string[]>((resolve, reject) => calls.push({ text, signal, resolve, reject }));
  return { calls, load };
}
const flush = () => vi.advanceTimersByTimeAsync(0);

describe("useAsyncOptions", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("waits for a quiet moment before asking, and asks once for a burst of typing", async () => {
    const m = manual();
    const a = useAsyncOptions(() => m.load, () => 250);
    a.ask("a");
    a.ask("ab");
    a.ask("abc");
    expect(a.status.value).toBe("loading");
    await vi.advanceTimersByTimeAsync(249);
    expect(m.calls).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(m.calls.map((c) => c.text)).toEqual(["abc"]);
    m.calls[0]?.resolve(["x"]);
    await flush();
    expect(a.answer.value).toEqual(["x"]);
    expect(a.status.value).toBe("idle");
  });

  it("asks at once when told to (the list just opened)", async () => {
    const m = manual();
    const a = useAsyncOptions(() => m.load, () => 250);
    a.ask("", true);
    await flush();
    expect(m.calls).toHaveLength(1);
  });

  it("drops an answer that arrives after a newer question, and aborts its request", async () => {
    const m = manual();
    const a = useAsyncOptions(() => m.load, () => 10);
    a.ask("a");
    await vi.advanceTimersByTimeAsync(10);
    a.ask("ab");
    await vi.advanceTimersByTimeAsync(10);
    expect(m.calls).toHaveLength(2);
    expect(m.calls[0]?.signal.aborted).toBe(true);
    m.calls[1]?.resolve(["new"]);
    await flush();
    m.calls[0]?.resolve(["old"]);
    await flush();
    expect(a.answer.value).toEqual(["new"]);
  });

  it("keeps a failure, and asks again on request", async () => {
    const m = manual();
    const a = useAsyncOptions(() => m.load, () => 10);
    a.ask("q", true);
    await flush();
    m.calls[0]?.reject(new Error("boom"));
    await flush();
    expect(a.status.value).toBe("error");
    expect((a.failure.value as Error).message).toBe("boom");
    a.again();
    await flush();
    expect(a.status.value).toBe("loading");
    expect(m.calls[1]?.text).toBe("q");
    m.calls[1]?.resolve(["ok"]);
    await flush();
    expect(a.status.value).toBe("idle");
    expect(a.failure.value).toBeNull();
  });

  it("forgets what is on its way when stopped", async () => {
    const m = manual();
    const a = useAsyncOptions(() => m.load, () => 10);
    a.ask("q", true);
    await flush();
    a.stop();
    m.calls[0]?.resolve(["late"]);
    await flush();
    expect(a.answer.value).toEqual([]);
    expect(a.status.value).toBe("idle");
  });
});
