import { CURRENCIES } from "./currencies";
import { minorDigits } from "./money";

/**
 * What the pickers show about a currency and a time zone, worked out with Intl so there is no table to keep: the currency's name in the person's language,
 * its symbol, its number of fraction digits (from the backend table); the zone's current offset from UTC.
 */

export interface CurrencyInfo {
  code: string;
  /** "US Dollar" in the language of the page. */
  name: string;
  /** "$" (the narrow symbol; the code itself when the currency has none). */
  symbol: string;
  /** Digits after the point: 2 for dollars, 0 for yen, 3 for dinar. */
  digits: number;
}

const currencyCache = new Map<string, CurrencyInfo[]>();

/** Every currency the backend accepts, with its name in the language, sorted by name. */
export function currencyList(locale: string): CurrencyInfo[] {
  const hit = currencyCache.get(locale);
  if (hit) return hit;
  let names: Intl.DisplayNames | null = null;
  try {
    names = new Intl.DisplayNames([locale], { type: "currency" });
  } catch {
    // an engine without DisplayNames: the code stands for the name
  }
  const list = CURRENCIES.map((code) => currencyInfo(code, locale, names)).sort((a, b) => a.name.localeCompare(b.name, locale));
  currencyCache.set(locale, list);
  return list;
}

export function currencyInfo(code: string, locale: string, names?: Intl.DisplayNames | null): CurrencyInfo {
  let name = code;
  let symbol = code;
  try {
    name = (names === undefined ? new Intl.DisplayNames([locale], { type: "currency" }) : names)?.of(code) ?? code;
  } catch {
    // keep the code
  }
  try {
    const f = new Intl.NumberFormat(locale, { style: "currency", currency: code, currencyDisplay: "narrowSymbol" });
    symbol = f.formatToParts(0).find((p) => p.type === "currency")?.value ?? code;
  } catch {
    // keep the defaults
  }
  return { code, name, symbol, digits: minorDigits(code) };
}

interface ZoneInfo {
  id: string;
  /** "UTC+03:00" now (daylight saving time included). */
  offset: string;
  /** Minutes east of UTC now, for sorting. */
  minutes: number;
}

/** Every zone the engine knows (the IANA names), plus UTC, which some engines leave out. */
export function zoneIds(): string[] {
  let ids: string[] = [];
  try {
    ids = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.("timeZone") ?? [];
  } catch {
    // fall through to the short list
  }
  if (ids.length === 0) ids = ["UTC", "Europe/London", "Europe/Istanbul", "Europe/Berlin", "America/New_York", "America/Los_Angeles", "Asia/Dubai", "Asia/Kolkata", "Asia/Tokyo", "Australia/Sydney"];
  return ids.includes("UTC") ? ids : ["UTC", ...ids];
}

/** The current offset of a zone as minutes east of UTC, or 0 for a name the engine rejects. */
export function zoneMinutes(id: string, at: Date = new Date()): number {
  try {
    const part = new Intl.DateTimeFormat("en-US", { timeZone: id, timeZoneName: "longOffset" }).formatToParts(at).find((p) => p.type === "timeZoneName")?.value ?? "GMT";
    const m = /^GMT(?:([+-])(\d{2}):(\d{2}))?$/.exec(part);
    if (!m || !m[1]) return 0;
    return (m[1] === "-" ? -1 : 1) * (Number(m[2]) * 60 + Number(m[3]));
  } catch {
    return 0;
  }
}

export function formatOffset(minutes: number): string {
  const sign = minutes < 0 ? "-" : "+";
  const abs = Math.abs(minutes);
  return `UTC${sign}${String(Math.floor(abs / 60)).padStart(2, "0")}:${String(abs % 60).padStart(2, "0")}`;
}

let zoneCache: ZoneInfo[] | null = null;

/** Every zone with its current offset, ordered by offset and then name. Worked out once per page. */
export function zoneList(): ZoneInfo[] {
  if (zoneCache) return zoneCache;
  const now = new Date();
  zoneCache = zoneIds()
    .map((id) => {
      const minutes = zoneMinutes(id, now);
      return { id, minutes, offset: formatOffset(minutes) };
    })
    .sort((a, b) => a.minutes - b.minutes || a.id.localeCompare(b.id));
  return zoneCache;
}

/**
 * The names people search for that differ from the ones engines list (Chromium still lists "Asia/Katmandu" and "Asia/Calcutta"). The zone chosen is the
 * engine's own name, which every IANA database (including the backend's) accepts; these words only help the search find it.
 */
export const ZONE_ALIASES: Record<string, string> = {
  "Asia/Katmandu": "Kathmandu",
  "Asia/Calcutta": "Kolkata",
  "Asia/Saigon": "Ho Chi Minh City Vietnam",
  "Asia/Rangoon": "Yangon Myanmar Burma",
  "Europe/Kiev": "Kyiv Ukraine",
  "America/Buenos_Aires": "Argentina",
  "Atlantic/Faeroe": "Faroe",
  "Pacific/Truk": "Chuuk",
  "Europe/Istanbul": "Turkey Türkiye",
};

/** The browser's own zone, or UTC. */
export function ownZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

/** Lower-case, accents removed: what search compares ("İstanbul" and "istanbul" match). */
export function fold(s: string): string {
  return s
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/İ/g, "i")
    .toLowerCase();
}
