import { describe, expect, it } from "vitest";

/**
 * A person's session is never an operator: the gateway answers 401 to a session on /admin/v1/*, and a 401 sends the person back to the sign-in page. So no
 * page that the product app mounts under /app/t/{tenant} may call an operator route. This reads the sources of those pages (and of what they import
 * from the console's own features) and fails when one mentions an /admin route or the operator's tenant lookup. The only allowed mention is
 * features/tenants/currency.ts, which asks the operator's tenant list only when no person is signed in.
 */
const sources = import.meta.glob(
  [
    "./*.{ts,vue}",
    "../customers/**/*.{ts,vue}",
    "../paymentlinks/**/*.{ts,vue}",
    "../credits/**/*.{ts,vue}",
    "../webhooks/**/*.{ts,vue}",
    "../packages/**/*.{ts,vue}",
    "../pricing/**/*.{ts,vue}",
    "../reports/**/*.{ts,vue}",
    "../invoices/ManualPaymentsCard.vue",
    "!../**/*.test.ts",
  ],
  { query: "?raw", import: "default", eager: true },
) as Record<string, string>;

describe("pages of the product app", () => {
  it("covers the pages that are mounted under /app/t/{tenant}", () => {
    const names = Object.keys(sources).map((p) => p.replace(/^\.\.?\//, ""));
    for (const expected of ["InvoicesPage.vue", "InvoiceDetail.vue", "PaymentsPage.vue", "billingApi.ts", "packages/PackagesPage.vue", "pricing/PricingPage.vue", "reports/ReportsPage.vue", "customers/CustomersPage.vue"]) {
      expect(names.some((n) => n.endsWith(expected)), expected).toBe(true);
    }
  });

  it("never calls an operator route", () => {
    const offenders = Object.entries(sources)
      .filter(([, text]) => text.includes("/admin/v1") || /\buseTenant\(/.test(text) || /\buseTenants\(/.test(text))
      .map(([path]) => path);
    expect(offenders).toEqual([]);
  });

  it("speaks the tenant API with the tenant named, not the operator client", () => {
    for (const path of ["./billingApi.ts"]) {
      const text = sources[path] ?? "";
      expect(text).toContain("tenantApi(");
      expect(text).not.toMatch(/\bapi\.(GET|POST|PUT|PATCH|DELETE)\("\/v1/);
    }
  });
});
