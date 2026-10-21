/** The currencies the backend accepts (pkg/money, every active ISO 4217 currency): a customer can be billed in them and a package priced in them. */
export const CURRENCIES = [
  "AED", "AFN", "ALL", "AMD", "AOA", "ARS", "AUD", "AWG", "AZN", "BAM", "BBD", "BDT", "BHD", "BIF", "BMD", "BND", "BOB", "BRL", "BSD", "BTN",
  "BWP", "BYN", "BZD", "CAD", "CDF", "CHF", "CLP", "CNY", "COP", "CRC", "CUP", "CVE", "CZK", "DJF", "DKK", "DOP", "DZD", "EGP", "ERN", "ETB",
  "EUR", "FJD", "FKP", "GBP", "GEL", "GHS", "GIP", "GMD", "GNF", "GTQ", "GYD", "HKD", "HNL", "HTG", "HUF", "IDR", "ILS", "INR", "IQD", "IRR",
  "ISK", "JMD", "JOD", "JPY", "KES", "KGS", "KHR", "KMF", "KPW", "KRW", "KWD", "KYD", "KZT", "LAK", "LBP", "LKR", "LRD", "LSL", "LYD", "MAD",
  "MDL", "MGA", "MKD", "MMK", "MNT", "MOP", "MRU", "MUR", "MVR", "MWK", "MXN", "MYR", "MZN", "NAD", "NGN", "NIO", "NOK", "NPR", "NZD", "OMR",
  "PAB", "PEN", "PGK", "PHP", "PKR", "PLN", "PYG", "QAR", "RON", "RSD", "RUB", "RWF", "SAR", "SBD", "SCR", "SDG", "SEK", "SGD", "SHP", "SLE",
  "SOS", "SRD", "SSP", "STN", "SYP", "SZL", "THB", "TJS", "TMT", "TND", "TOP", "TRY", "TTD", "TWD", "TZS", "UAH", "UGX", "USD", "UYU", "UZS",
  "VES", "VND", "VUV", "WST", "XAF", "XCD", "XCG", "XOF", "XPF", "YER", "ZAR", "ZMW", "ZWG",
] as const;
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
