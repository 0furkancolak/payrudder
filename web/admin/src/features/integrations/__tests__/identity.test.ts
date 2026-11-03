import { describe, expect, it } from "vitest";
import { IDENTITY_CHECKS, IDENTITY_STATUSES, identityTone } from "../identity";

describe("how a sender-identity verdict is painted", () => {
  it("paints every status the server can answer", () => {
    expect(IDENTITY_STATUSES.map(identityTone)).toEqual(["success", "warning", "danger", "danger", "neutral", "neutral"]);
  });
  it("lists the three records, SPF first", () => {
    expect([...IDENTITY_CHECKS]).toEqual(["spf", "dkim", "dmarc"]);
  });
});
