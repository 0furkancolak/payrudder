/** The currencies the backend accepts (pkg/money): the ones a customer can be billed in and a package priced in. */
export const CURRENCIES = ["CHF", "EUR", "GBP", "JPY", "KWD", "TRY", "USD"] as const;
export type Currency = (typeof CURRENCIES)[number];

/** The countries the console offers in routing rules: any ISO 3166-1 alpha-2 code is valid, these are shortcuts. */
export const COUNTRY_HINTS = ["TR", "DE", "FR", "ES", "GB", "US", "NL", "IT", "CH", "JP"] as const;

/** The payment providers Quotify has adapters for, and whether the provider runs subscriptions itself. */
export const PROVIDERS = [
  { name: "iyzico", runsSubscriptions: false },
  { name: "paddle", runsSubscriptions: true },
] as const;
