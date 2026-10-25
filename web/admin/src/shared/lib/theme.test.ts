import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

describe("isDarkFor", () => {
  it("follows the system only while the choice is 'system'", async () => {
    vi.stubGlobal("window", { localStorage: { getItem: () => null, setItem: () => undefined, removeItem: () => undefined } });
    const { isDarkFor } = await import("./theme");
    expect(isDarkFor("system", true)).toBe(true);
    expect(isDarkFor("system", false)).toBe(false);
    expect(isDarkFor("dark", false)).toBe(true);
    expect(isDarkFor("light", true)).toBe(false);
    vi.unstubAllGlobals();
  });
});

describe("the theme the header toggle shows", () => {
  beforeEach(() => vi.resetModules());
  afterEach(() => vi.unstubAllGlobals());

  it("changes when the choice is made elsewhere (the settings page calls applyTheme)", async () => {
    const store = new Map<string, string>();
    vi.stubGlobal("window", {
      localStorage: {
        getItem: (k: string) => store.get(k) ?? null,
        setItem: (k: string, v: string) => void store.set(k, v),
        removeItem: (k: string) => void store.delete(k),
      },
    });
    const attrs = new Map<string, string>();
    vi.stubGlobal("document", { documentElement: { setAttribute: (k: string, v: string) => attrs.set(k, v), removeAttribute: (k: string) => attrs.delete(k) } });
    const { theme, applyTheme, isDarkFor } = await import("./theme");
    expect(isDarkFor(theme.value, false)).toBe(false);
    applyTheme("dark");
    expect(isDarkFor(theme.value, false)).toBe(true);
    expect(attrs.get("data-theme")).toBe("dark");
    expect(store.get("payrudder.theme")).toBe("dark");
    applyTheme("system");
    expect(isDarkFor(theme.value, true)).toBe(true);
    expect(store.has("payrudder.theme")).toBe(false);
  });
});
