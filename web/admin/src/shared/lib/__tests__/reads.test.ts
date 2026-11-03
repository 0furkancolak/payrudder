import { describe, expect, it, vi } from "vitest";
import { readOf, resultOf, together, type Read } from "../reads";

const read = (over: Partial<Read> = {}): Read => ({ error: null, loading: false, fetching: false, retry: vi.fn(), ...over });

describe("together", () => {
  it("is loading while any read is, and failed with the first error", () => {
    expect(together([read(), read({ loading: true })]).loading).toBe(true);
    expect(together([read(), read()]).error).toBeNull();
    expect(together([read(), read({ error: "a" }), read({ error: "b" })]).error).toBe("a");
  });
  it("retries only the reads that failed", async () => {
    const ok = read();
    const bad = read({ error: "x" });
    await together([ok, bad]).retry();
    expect(bad.retry).toHaveBeenCalledOnce();
    expect(ok.retry).not.toHaveBeenCalled();
  });
});

describe("readOf", () => {
  it("reads the refs of a query when asked, not when made", () => {
    const q = { error: { value: null as unknown }, isLoading: { value: true }, isFetching: { value: true }, refetch: vi.fn() };
    expect(readOf(q)).toMatchObject({ error: null, loading: true, fetching: true });
    q.error.value = "down";
    q.isLoading.value = false;
    expect(readOf(q)).toMatchObject({ error: "down", loading: false });
    void readOf(q).retry();
    expect(q.refetch).toHaveBeenCalledOnce();
  });
});

describe("resultOf", () => {
  it("reads a plain result of useQueries", () => {
    const q = { error: new Error("down"), isLoading: false, isFetching: true, refetch: vi.fn() };
    expect(resultOf(q)).toMatchObject({ error: q.error, loading: false, fetching: true });
    void resultOf(q).retry();
    expect(q.refetch).toHaveBeenCalledOnce();
  });
});
