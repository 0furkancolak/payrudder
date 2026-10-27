import { describe, expect, it } from "vitest";
import { isDeadSession } from "./client";

describe("isDeadSession", () => {
  it("ends the session for an invalid one and for an unnamed 401", () => {
    expect(isDeadSession({ error: { key: "identity.session.invalid" } })).toBe(true);
    expect(isDeadSession({ error: { message: "nope" } })).toBe(true);
    expect(isDeadSession(undefined)).toBe(true);
  });
  it("keeps the session for a client mistake such as a missing tenant", () => {
    expect(isDeadSession({ error: { key: "identity.session.act_as_required" } })).toBe(false);
  });
});
