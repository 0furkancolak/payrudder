import { describe, expect, it } from "vitest";
import { LANGS, keyHintOf, snippets, type Inputs } from "./snippets";

const base: Inputs = { baseUrl: "https://api.example.com/", meter: "ai_tokens", customer: "cus_42", keyHint: "qk_test_" };

describe("snippets", () => {
  it("has an install line, a send and a webhook snippet for every language", () => {
    for (const l of LANGS) {
      const s = snippets(l, base);
      expect(s.install.length, l).toBeGreaterThan(5);
      expect(s.send.length, l).toBeGreaterThan(20);
      expect(s.webhook.length, l).toBeGreaterThan(20);
    }
  });

  it("puts the app's own values in the send snippet and drops a trailing slash of the address", () => {
    for (const l of LANGS) {
      const s = snippets(l, base).send;
      expect(s, l).toContain("ai_tokens");
      expect(s, l).toContain("cus_42");
      expect(s, l).toContain("https://api.example.com");
      expect(s, l).not.toContain("api.example.com/\"");
    }
    expect(snippets("curl", base).send).toContain("https://api.example.com/v1/events");
  });

  it("never writes a real key: only the shape of the environment's key", () => {
    expect(snippets("typescript", base).send).toContain("qk_test_...");
    expect(keyHintOf(true)).toBe("qk_live_");
    expect(keyHintOf(false)).toBe("qk_test_");
  });

  it("cannot let a single quote in a value end the shell string of the curl example", () => {
    const s = snippets("curl", { ...base, customer: "x'; echo pwned; '" }).send;
    expect(s).toContain("x'\\''; echo pwned; '\\''");
    expect(s).not.toContain("x'; echo");
  });

  it("keeps a hostile customer id inside its string", () => {
    const evil = { ...base, customer: 'x"; rm -rf /; echo "', meter: "m\nnext" };
    for (const l of LANGS) {
      const s = snippets(l, evil).send;
      expect(s, l).not.toContain('x"; rm');
      expect(s, l).not.toContain("m\nnext");
    }
  });
});
