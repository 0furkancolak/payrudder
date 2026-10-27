import { describe, expect, it } from "vitest";
import { ApiError } from "@/shared/api/client";
import { appLimitReached, isPayable, isUnlimited, isUpgradeError, limitOf, offerMove, parseLimit, quotaPercent, quotaTone, stateTone } from "./lib";

describe("quotas", () => {
  it("treats 0 as unlimited and never fills its bar", () => {
    expect(isUnlimited(0)).toBe(true);
    expect(isUnlimited(undefined)).toBe(true);
    expect(isUnlimited(5)).toBe(false);
    expect(quotaPercent({ used: 900, limit: 0, unlimited: true })).toBe(0);
    expect(quotaPercent({ used: 900, limit: 0 })).toBe(0);
  });

  it("works out the percent and clamps it", () => {
    expect(quotaPercent({ used: 1, limit: 4 })).toBe(25);
    expect(quotaPercent({ used: 9, limit: 4 })).toBe(100);
    expect(quotaPercent({ used: undefined, limit: 4 })).toBe(0);
  });

  it("is amber from 80 percent and red when over the limit", () => {
    expect(quotaTone({ used: 1, limit: 10 })).toBe("ok");
    expect(quotaTone({ used: 8, limit: 10 })).toBe("warning");
    expect(quotaTone({ used: 10, limit: 10 })).toBe("danger");
    expect(quotaTone({ used: 3, limit: 10, over_limit: true })).toBe("danger");
    expect(quotaTone({ used: 99, limit: 0, unlimited: true })).toBe("ok");
  });
});

describe("offers", () => {
  const free = { current: false, base_fee_minor: 0 };
  const pro = { current: true, base_fee_minor: 2900 };
  const scale = { current: false, base_fee_minor: 9900 };
  it("tells upgrades from downgrades by price", () => {
    expect(offerMove(pro, pro)).toBe("current");
    expect(offerMove(scale, pro)).toBe("upgrade");
    expect(offerMove(free, pro)).toBe("downgrade");
    expect(offerMove(scale, undefined)).toBe("upgrade");
  });
  it("finds the limit of a resource", () => {
    expect(limitOf({ limits: [{ resource: "apps", limit: 3, mode: "hard" }] }, "apps")?.limit).toBe(3);
    expect(limitOf({ limits: [] }, "apps")).toBeUndefined();
  });
});

describe("errors and states", () => {
  it("offers the upgrade for a quota error only", () => {
    expect(isUpgradeError(new ApiError("x", "RESOURCE_EXHAUSTED", "plans.saas.quota.apps"))).toBe(true);
    expect(isUpgradeError(new ApiError("x", "RESOURCE_EXHAUSTED", "gateway.saas.events_quota"))).toBe(true);
    expect(isUpgradeError(new ApiError("x", "RESOURCE_EXHAUSTED", "identity.app.quota"))).toBe(false);
    expect(isUpgradeError(new ApiError("x", "CONFLICT", "plans.saas.downgrade_blocked"))).toBe(false);
    expect(isUpgradeError(null)).toBe(false);
  });
  it("colours the state", () => {
    expect(stateTone("trialing")).toBe("info");
    expect(stateTone("past_due")).toBe("danger");
    expect(stateTone("none")).toBe("neutral");
  });
  it("pays open invoices only", () => {
    expect(isPayable("open")).toBe(true);
    expect(isPayable("paid")).toBe(false);
  });
  it("parses limit inputs", () => {
    expect(parseLimit("")).toBeUndefined();
    expect(parseLimit(" 12 ")).toBe(12);
    expect(parseLimit("0")).toBe(0);
    expect(parseLimit("-1")).toBeNull();
    expect(parseLimit("1.5")).toBeNull();
  });
});

describe("appLimitReached", () => {
  it("reports a hard apps limit that is used up", () => {
    expect(appLimitReached([{ resource: "apps", used: 1, limit: 1, unlimited: false, mode: "hard" }])).toEqual({ used: 1, limit: 1 });
  });
  it("is silent while there is room, for soft limits, unlimited plans and other resources", () => {
    expect(appLimitReached([{ resource: "apps", used: 0, limit: 1, unlimited: false, mode: "hard" }])).toBeUndefined();
    expect(appLimitReached([{ resource: "apps", used: 5, limit: 1, unlimited: false, mode: "soft" }])).toBeUndefined();
    expect(appLimitReached([{ resource: "apps", used: 5, limit: 0, unlimited: true, mode: "hard" }])).toBeUndefined();
    expect(appLimitReached([{ resource: "members", used: 5, limit: 1, unlimited: false, mode: "hard" }])).toBeUndefined();
    expect(appLimitReached(undefined)).toBeUndefined();
  });
});
