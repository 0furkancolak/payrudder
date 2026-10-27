import { describe, expect, it } from "vitest";
import { answerBody, isInsecureUrl, plainAnswer } from "./lib";

describe("isInsecureUrl", () => {
  it("is true for plain http only", () => {
    expect(isInsecureUrl("http://example.com/hook")).toBe(true);
    expect(isInsecureUrl("  HTTP://example.com")).toBe(true);
    expect(isInsecureUrl("https://example.com/hook")).toBe(false);
    expect(isInsecureUrl("")).toBe(false);
    expect(isInsecureUrl(undefined)).toBe(false);
  });
});

describe("plainAnswer", () => {
  it("takes the markup out of an HTML answer and keeps one line", () => {
    const a = plainAnswer("<!doctype html><html><head><title>x</title><style>p{}</style></head><body><h1>Method   Not\nAllowed</h1></body></html>");
    expect(a.text).not.toContain("<");
    expect(a.full).toMatch(/Method Not Allowed$/);
  });
  it("cuts a long answer and keeps the whole text for the tooltip", () => {
    const a = plainAnswer("a".repeat(300), 50);
    expect(a.text).toHaveLength(50);
    expect(a.text.endsWith("…")).toBe(true);
    expect(a.full).toHaveLength(300);
  });
  it("is empty for nothing", () => {
    expect(plainAnswer(undefined)).toEqual({ text: "", full: "" });
  });
});

describe("answerBody", () => {
  it("drops the status prefix the status badge already shows", () => {
    expect(answerBody("HTTP 405: nope", 405)).toBe("nope");
    expect(answerBody("connection refused", undefined)).toBe("connection refused");
    expect(answerBody("connection refused", "")).toBe("connection refused");
  });
});
