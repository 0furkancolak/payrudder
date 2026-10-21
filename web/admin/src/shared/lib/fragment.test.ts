import { afterEach, describe, expect, it, vi } from "vitest";
import { takeFragmentToken } from "./fragment";

function fakeWindow(hash: string) {
  const replaceState = vi.fn();
  vi.stubGlobal("window", { location: { hash, pathname: "/reset", search: "?a=1" }, history: { state: null, replaceState } });
  return replaceState;
}

afterEach(() => vi.unstubAllGlobals());

describe("takeFragmentToken", () => {
  it("returns the token of the link and removes the fragment from the address bar", () => {
    const replaceState = fakeWindow("#token=qr_abc-DEF_123");
    expect(takeFragmentToken()).toBe("qr_abc-DEF_123");
    expect(replaceState).toHaveBeenCalledWith(null, "", "/reset?a=1");
  });

  it("returns an empty string when the link carries none, and leaves the address alone", () => {
    const replaceState = fakeWindow("");
    expect(takeFragmentToken()).toBe("");
    expect(replaceState).not.toHaveBeenCalled();
  });

  it("ignores other fragment fields and trims", () => {
    fakeWindow("#x=1&token=%20qv_ok%20");
    expect(takeFragmentToken()).toBe("qv_ok");
  });
});
