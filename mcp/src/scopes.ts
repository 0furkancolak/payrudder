/**
 * The permission a GET of the tenant API needs: the same rule as the gateway's pkg/apikey RequiredPermission, for the reads only (a tool that writes names its permission
 * itself). The OAuth mode uses it to answer `403 insufficient_scope` naming the scope, before the tool is run; the API checks again on every call, so a mistake here can
 * only refuse early or defer to the API, never allow. `undefined` for a path no rule covers (the API then decides, and refuses all but an admin key).
 */
export function readPermission(path: string): string | undefined {
  const segs = path.replace(/^\/v1\//, "").split("/");
  const r = (resource: string) => `${resource}:read`;
  switch (segs[0]) {
    case "customers":
      switch (segs[2]) {
        case "credits":
          return r("credits");
        case "usage":
        case "entitlements":
          return r("usage");
        case "payments":
        case "portal-links":
          return r("invoices");
        case "coupons":
          return r("subscriptions");
        case "referral":
          return r("affiliates");
      }
      return r("customers");
    case "subscriptions":
      return r("subscriptions");
    case "promotions":
    case "plans":
    case "price-changes":
    case "coupons":
    case "coupon-redemptions":
    case "promotion-codes":
    case "credit-packs":
    case "meters":
      return r("catalog");
    case "invoices":
    case "credit-notes":
    case "e-invoices":
    case "payment-links":
    case "payments":
    case "disputes":
      return r("invoices");
    case "reports":
    case "analytics":
      return r("reports");
    case "accounting":
    case "expenses":
    case "expense-inbox":
    case "expense-rules":
      return r("accounting");
    case "audit-events":
      return r("audit");
    case "webhook-endpoints":
    case "webhook-deliveries":
      return r("webhooks");
    case "affiliates":
      return r("affiliates");
    case "dunning-settings":
    case "mail-settings":
    case "mail-previews":
    case "routing-rules":
    case "tax-rates":
    case "tenant":
    case "credits":
      return r("settings");
    case "mail-suppressions":
    case "mail-messages":
    case "contract-acceptances":
    case "blocklist":
      return r("customers");
    case "assets":
      return r("assets");
    case "contracts":
      return r("contracts");
  }
  return undefined;
}
