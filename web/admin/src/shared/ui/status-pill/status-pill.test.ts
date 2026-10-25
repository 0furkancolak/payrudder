import { describe, expect, it } from "vitest";
import { toneOf } from "./index";

describe("toneOf (DESIGN.md section 3)", () => {
  it.each([
    ["succeeded", "success"],
    ["paid", "success"],
    ["settled", "success"],
    ["pending", "warning"],
    ["processing", "warning"],
    ["requires_action", "warning"],
    ["failed", "danger"],
    ["declined", "danger"],
    ["disputed", "danger"],
    ["refunded", "neutral"],
    ["void", "neutral"],
    ["draft", "neutral"],
    ["routed", "info"],
    ["retrying", "info"],
    ["something-else", "neutral"],
  ])("%s is %s", (status, tone) => {
    expect(toneOf(status)).toBe(tone);
  });
});
