import { describe, expect, it } from "vitest";
import en from "@/locales/en.json";
import { EVENT_ERROR_CODES, eventErrorHintKey } from "../eventErrors";

describe("hints for a rejected usage event", () => {
  it("has a hint, in English, for every code the console lists, and names the locale key of each", () => {
    const hints = (en.connect as unknown as { eventErrors: Record<string, string> }).eventErrors;
    expect(Object.keys(hints).sort()).toEqual([...EVENT_ERROR_CODES].sort());
    for (const code of EVENT_ERROR_CODES) {
      expect(hints[code]?.length, code).toBeGreaterThan(20);
      expect(eventErrorHintKey(code)).toBe(`connect.eventErrors.${code}`);
    }
  });

  it("gives no key for a code it does not know or for none", () => {
    expect(eventErrorHintKey("SOMETHING_NEW")).toBeNull();
    expect(eventErrorHintKey(undefined)).toBeNull();
    expect(eventErrorHintKey("")).toBeNull();
  });
});
