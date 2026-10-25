import { afterEach, describe, expect, it, vi } from "vitest";
import { safeGet, safeSet } from "./storage";

afterEach(() => vi.unstubAllGlobals());

describe("storage that may be blocked", () => {
  it("reads and writes the area asked for, and null forgets a key", () => {
    const local = new Map<string, string>();
    const session = new Map<string, string>();
    const fake = (m: Map<string, string>) => ({ getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k) });
    vi.stubGlobal("window", { localStorage: fake(local), sessionStorage: fake(session) });
    expect(safeSet("local", "a", "1")).toBe(true);
    expect(safeSet("session", "a", "2")).toBe(true);
    expect(safeGet("local", "a")).toBe("1");
    expect(safeGet("session", "a")).toBe("2");
    expect(safeSet("local", "a", null)).toBe(true);
    expect(safeGet("local", "a")).toBeNull();
    expect(safeGet("session", "a")).toBe("2");
  });

  it("finds nothing and drops writes when storage throws", () => {
    const blocked = {
      get localStorage(): never {
        throw new Error("blocked");
      },
      get sessionStorage(): never {
        throw new Error("blocked");
      },
    };
    vi.stubGlobal("window", blocked);
    expect(safeGet("local", "a")).toBeNull();
    expect(safeSet("session", "a", "1")).toBe(false);
  });
});
