import "./prefill";

const DEFAULT_BRAND = "Quotify";

/** The name the product goes by: served by /env.js (BRAND_NAME), else the default. */
export function brandName(): string {
  return ((typeof window === "undefined" ? undefined : window.__QUOTIFY__?.brand) ?? "").trim() || DEFAULT_BRAND;
}

/** Every text of a locale with the default product name replaced by the configured one. */
export function branded<T>(value: T, name = brandName()): T {
  if (name === DEFAULT_BRAND) return value;
  if (typeof value === "string") return value.replaceAll(DEFAULT_BRAND, name) as T;
  if (Array.isArray(value)) return value.map((v) => branded(v, name)) as T;
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, branded(v, name)])) as T;
  return value;
}
