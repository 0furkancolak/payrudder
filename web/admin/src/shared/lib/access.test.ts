import { describe, expect, it } from "vitest";
import { createApp } from "vue";
import { useCanWrite } from "./access";
import { setPersonToken } from "./session";

const can = () => createApp({}).runWithContext(() => useCanWrite());

// Outside a provider (the operator's console) a page may write unless the server called the session a read-only support operator.
describe("useCanWrite in the operator console", () => {
  it("is true for no session, a person, an operator admin and false for a support operator", () => {
    expect(can().value).toBe(true);
    setPersonToken("qs_a", false);
    expect(can().value).toBe(true);
    setPersonToken("qs_a", true, "operator_admin");
    expect(can().value).toBe(true);
    setPersonToken("qs_a", true, "operator_support");
    expect(can().value).toBe(false);
    setPersonToken(null);
    expect(can().value).toBe(true);
  });
});
