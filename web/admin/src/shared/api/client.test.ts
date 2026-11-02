import { describe, expect, it, vi } from "vitest";
import { onSetupRequired } from "@/shared/lib/session";
import { SETUP_REQUIRED_KEY, isDeadSession, watchSession } from "./client";

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

describe("watchSession", () => {
  const answer = (status: number, body: unknown) => watchSession.onResponse?.({ response: new Response(JSON.stringify(body), { status }) } as never);

  it("tells the app about the 403 that holds a person on the security page, and about no other refusal", async () => {
    const heard = vi.fn();
    const off = onSetupRequired(heard);
    await answer(403, { error: { key: "identity.account.forbidden" } });
    await answer(403, { error: { key: "gateway.auth.permission_denied" } });
    await answer(403, "not json");
    expect(heard).not.toHaveBeenCalled();
    await answer(403, { error: { key: SETUP_REQUIRED_KEY } });
    expect(heard).toHaveBeenCalledTimes(1);
    off();
    await answer(403, { error: { key: SETUP_REQUIRED_KEY } });
    expect(heard).toHaveBeenCalledTimes(1);
  });
});
