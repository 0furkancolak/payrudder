import { describe, expect, it } from "vitest";
import { newQuota, type Meter } from "@/features/meters/quota";
import { buildUsage, newPrice } from "./usage";

const meters: Meter[] = [
  { code: "api_calls", aggregation: "AGGREGATION_COUNT", unit_label: "calls", quantity_scale: 0 },
  { code: "gb_hours", aggregation: "AGGREGATION_SUM", unit_label: "GB-h", quantity_scale: 3 },
];

describe("buildUsage", () => {
  it("turns major units into the API's minor ones and keeps quantities as typed", () => {
    const r = buildUsage(
      [
        { ...newPrice("api_calls"), included: "1000", unitPrice: "0.0002" },
        {
          ...newPrice("gb_hours"),
          model: "tiered",
          tiers: [
            { upTo: "10.5", unitPrice: "0", flatFee: "" },
            { upTo: "", unitPrice: "0.25", flatFee: "1" },
          ],
        },
      ],
      [{ meter: "api_calls", ...newQuota(), limit: "5000", window: "QUOTA_WINDOW_DAY", mode: "QUOTA_MODE_SOFT", thresholds: "50, 80, 100" }],
      meters,
      "USD",
    );
    expect(r).toEqual({
      prices: [
        { meter_code: "api_calls", included: "1000", per_unit: { unit_price: "0.02" } },
        { meter_code: "gb_hours", tiered: { tiers: [{ up_to: "10.5", unit_price: "0" }, { unit_price: "25", flat_fee_minor: "100" }] } },
      ],
      entitlements: [{ meter_code: "api_calls", limit: "5000", window: "QUOTA_WINDOW_DAY", mode: "QUOTA_MODE_SOFT", alert_thresholds_pct: [50, 80, 100] }],
    });
  });

  it("prices packages of units", () => {
    const r = buildUsage([{ ...newPrice("api_calls"), model: "package", packageSize: "1000", packagePrice: "5" }], [], meters, "TRY");
    expect(r).toEqual({ prices: [{ meter_code: "api_calls", package: { package_size: "1000", package_price_minor: "500" } }], entitlements: [] });
  });

  it("names the first wrong row", () => {
    const cases: [Parameters<typeof buildUsage>[0], Parameters<typeof buildUsage>[1], string][] = [
      [[{ ...newPrice(""), unitPrice: "1" }], [], "meter"],
      [[{ ...newPrice("api_calls"), unitPrice: "1" }, { ...newPrice("api_calls"), unitPrice: "1" }], [], "duplicate"],
      [[{ ...newPrice("api_calls"), unitPrice: "x" }], [], "unitPrice"],
      [[{ ...newPrice("api_calls"), unitPrice: "1", included: "1.5" }], [], "included"],
      [[{ ...newPrice("api_calls"), model: "package", packageSize: "0", packagePrice: "1" }], [], "packageSize"],
      [[], [{ meter: "gb_hours", ...newQuota(), limit: "1.2345" }], "limit"],
      [[], [{ meter: "gb_hours", ...newQuota(), limit: "1", thresholds: "200, 100" }], "thresholds"],
      [[], [{ meter: "gb_hours", ...newQuota(), limit: "1", mode: "QUOTA_MODE_SOFT" }], "softNeedsPrice"],
    ];
    for (const [prices, quotas, key] of cases) {
      const r = buildUsage(prices, quotas, meters, "USD");
      expect("error" in r && r.error.key, key).toBe(key);
    }
  });
});
