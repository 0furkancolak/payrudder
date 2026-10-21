/** The currencies the backend accepts (pkg/money): the ones a customer can be billed in and a package priced in. */
export const CURRENCIES = ["CHF", "EUR", "GBP", "JPY", "KWD", "TRY", "USD"] as const;
export type Currency = (typeof CURRENCIES)[number];

/** The countries the console offers in routing rules: any ISO 3166-1 alpha-2 code is valid, these are shortcuts. */
export const COUNTRY_HINTS = ["TR", "DE", "FR", "ES", "GB", "US", "NL", "IT", "CH", "JP"] as const;

/**
 * The payment providers Quotify has adapters for, whether the provider runs subscriptions itself, and whether the API's routing rules and provider
 * migrations name it (the OpenAPI enums list iyzico and paddle only: bank transfer is a tenant's default account, not a routing target).
 */
export const PROVIDERS = [
  { name: "iyzico", runsSubscriptions: false, routable: true },
  { name: "paddle", runsSubscriptions: true, routable: true },
  { name: "banktransfer", runsSubscriptions: false, routable: false },
] as const;

/** The providers a routing rule or a provider move can name. */
export const ROUTABLE_PROVIDERS = PROVIDERS.filter((p) => p.routable);
