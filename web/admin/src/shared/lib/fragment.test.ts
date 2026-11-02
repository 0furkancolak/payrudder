import { afterEach, describe, expect, it, vi } from "vitest";
import { parseChallengeFragment, takeFragmentChallenge, takeFragmentToken } from "./fragment";

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

describe("the challenge of a sign-in with Google", () => {
  it("is read from the fragment: the challenge and when it ends", () => {
    expect(parseChallengeFragment("#challenge=qc_abc-DEF_123&expires=2026-11-02T10:05:00Z")).toEqual({ challenge: "qc_abc-DEF_123", expires: "2026-11-02T10:05:00Z" });
    expect(parseChallengeFragment("#expires=x&challenge=%20qc_ok%20")).toEqual({ challenge: "qc_ok", expires: "x" });
  });

  it("is nothing when the fragment holds a token, no challenge, or something that is not one", () => {
    expect(parseChallengeFragment("#token=qs_abc&expires=x")).toBeNull();
    expect(parseChallengeFragment("")).toBeNull();
    expect(parseChallengeFragment("#challenge=not-a-challenge")).toBeNull();
    expect(parseChallengeFragment("#challenge=qc_has space")).toBeNull();
  });

  it("is removed from the address bar once taken, and a fragment with a token is left for takeFragmentToken", () => {
    const replaceState = fakeWindow("#challenge=qc_abc&expires=2026-11-02T10:05:00Z");
    expect(takeFragmentChallenge()?.challenge).toBe("qc_abc");
    expect(replaceState).toHaveBeenCalledWith(null, "", "/reset?a=1");
    const untouched = fakeWindow("#token=qs_abc");
    expect(takeFragmentChallenge()).toBeNull();
    expect(untouched).not.toHaveBeenCalled();
    expect(takeFragmentToken()).toBe("qs_abc");
  });
});
