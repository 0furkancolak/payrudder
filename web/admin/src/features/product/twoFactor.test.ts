import { describe, expect, it } from "vitest";
import { RECOVERY_FILE_NAME, SECURITY_PATH, isChallenge, recoveryFileText, setupRedirect } from "./twoFactor";

describe("isChallenge", () => {
  it("tells the challenge of the second step from a session", () => {
    expect(isChallenge({ second_factor_required: true, challenge: "qc_x", challenge_expire_time: null })).toBe(true);
    expect(isChallenge({ user: null, memberships: [], token: "qs_x", expire_time: null })).toBe(false);
  });
});

describe("setupRedirect", () => {
  it("sends a person who must set up two-factor sign-in to the security page from every other page", () => {
    for (const path of ["/app", "/app/settings", "/app/t/abc/customers", "/operator", "/tenants/1", "/app/security/extra"]) expect(setupRedirect(path, true), path).toBe(SECURITY_PATH);
  });
  it("leaves them on the security page, and on the pages a mailed link opens", () => {
    for (const path of ["/app/security", "/app/security/", "/signin/complete", "/reset", "/verify", "/invite"]) expect(setupRedirect(path, true), path).toBeNull();
  });
  it("does nothing for anybody else", () => {
    expect(setupRedirect("/app", false)).toBeNull();
    expect(setupRedirect("/app/settings", false)).toBeNull();
  });
});

describe("recoveryFileText", () => {
  const codes = Array.from({ length: 10 }, (_, i) => `abcd-${String(i).repeat(4)}`);
  const text = recoveryFileText({ title: "Payrudder recovery codes for ada@example.com, 2026-11-02", note: "Each code works once.", codes });

  it("starts with the header line (address and day), then the note, then the codes numbered one to a line", () => {
    const lines = text.split("\n");
    expect(lines[0]).toBe("Payrudder recovery codes for ada@example.com, 2026-11-02");
    expect(lines[2]).toBe("Each code works once.");
    expect(lines.slice(4, 14)).toEqual(codes.map((c, i) => `${String(i + 1).padStart(2, " ")}. ${c}`));
    expect(text.endsWith("\n")).toBe(true);
  });
  it("holds every code once and the file has the name the page promises", () => {
    for (const c of codes) expect(text.split(c)).toHaveLength(2);
    expect(RECOVERY_FILE_NAME).toBe("payrudder-recovery-codes.txt");
  });
});
