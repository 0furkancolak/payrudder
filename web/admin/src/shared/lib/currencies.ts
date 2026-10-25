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

/** What a form starts with before the tenant's own currency is known (it switches as soon as that arrives). Never used to read an amount a person typed: that waits for the real currency. */
export const FALLBACK_CURRENCY: Currency = "USD";

/**
 * The minor-unit exponent of every currency above, copied from pkg/money (the backend table that drives every invoice amount; tests/currencies_test.go
 * fails when the two differ). Not taken from Intl: ICU disagrees with the backend for HUF, IDR, COP, IQD and a dozen more, and a wrong exponent shows
 * an amount 100 to 1000 times off.
 */
export const CURRENCY_EXPONENTS: Record<Currency, number> = {
  AED: 2, AFN: 2, ALL: 2, AMD: 2, AOA: 2, ARS: 2, AUD: 2, AWG: 2, AZN: 2, BAM: 2, BBD: 2, BDT: 2, BHD: 3, BIF: 0, BMD: 2, BND: 2, BOB: 2,
  BRL: 2, BSD: 2, BTN: 2, BWP: 2, BYN: 2, BZD: 2, CAD: 2, CDF: 2, CHF: 2, CLP: 0, CNY: 2, COP: 2, CRC: 2, CUP: 2, CVE: 2, CZK: 2, DJF: 0,
  DKK: 2, DOP: 2, DZD: 2, EGP: 2, ERN: 2, ETB: 2, EUR: 2, FJD: 2, FKP: 2, GBP: 2, GEL: 2, GHS: 2, GIP: 2, GMD: 2, GNF: 0, GTQ: 2, GYD: 2,
  HKD: 2, HNL: 2, HTG: 2, HUF: 2, IDR: 2, ILS: 2, INR: 2, IQD: 3, IRR: 2, ISK: 0, JMD: 2, JOD: 3, JPY: 0, KES: 2, KGS: 2, KHR: 2, KMF: 0,
  KPW: 2, KRW: 0, KWD: 3, KYD: 2, KZT: 2, LAK: 2, LBP: 2, LKR: 2, LRD: 2, LSL: 2, LYD: 3, MAD: 2, MDL: 2, MGA: 2, MKD: 2, MMK: 2, MNT: 2,
  MOP: 2, MRU: 2, MUR: 2, MVR: 2, MWK: 2, MXN: 2, MYR: 2, MZN: 2, NAD: 2, NGN: 2, NIO: 2, NOK: 2, NPR: 2, NZD: 2, OMR: 3, PAB: 2, PEN: 2,
  PGK: 2, PHP: 2, PKR: 2, PLN: 2, PYG: 0, QAR: 2, RON: 2, RSD: 2, RUB: 2, RWF: 0, SAR: 2, SBD: 2, SCR: 2, SDG: 2, SEK: 2, SGD: 2, SHP: 2,
  SLE: 2, SOS: 2, SRD: 2, SSP: 2, STN: 2, SYP: 2, SZL: 2, THB: 2, TJS: 2, TMT: 2, TND: 3, TOP: 2, TRY: 2, TTD: 2, TWD: 2, TZS: 2, UAH: 2,
  UGX: 0, USD: 2, UYU: 2, UZS: 2, VES: 2, VND: 0, VUV: 0, WST: 2, XAF: 0, XCD: 2, XCG: 2, XOF: 0, XPF: 0, YER: 2, ZAR: 2, ZMW: 2, ZWG: 2,
};

/** The countries the console offers in routing rules: any ISO 3166-1 alpha-2 code is valid, these are shortcuts. */
export const COUNTRY_HINTS = ["TR", "DE", "FR", "ES", "GB", "US", "NL", "IT", "CH", "JP"] as const;

/**
 * The payment providers Payrudder has adapters for, whether the provider runs subscriptions itself, and whether the API's routing rules and provider
 * migrations name it (the OpenAPI enums list iyzico and paddle only: bank transfer is a tenant's default account, not a routing target).
 */
export const PROVIDERS = [
  { name: "iyzico", runsSubscriptions: false, routable: true },
  { name: "paddle", runsSubscriptions: true, routable: true },
  { name: "banktransfer", runsSubscriptions: false, routable: false },
] as const;

/** The providers a routing rule or a provider move can name. */
export const ROUTABLE_PROVIDERS = PROVIDERS.filter((p) => p.routable);
