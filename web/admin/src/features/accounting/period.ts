import { ymd } from "@/shared/lib/dates";

/** The periods the accounting page offers. Days are the browser's calendar days, sent as 2026-10-21; the server counts UTC days. */
export type Preset = "thisMonth" | "lastMonth" | "last30" | "thisYear" | "custom";
export const PRESETS: readonly Preset[] = ["thisMonth", "lastMonth", "last30", "thisYear", "custom"];

export interface Range {
  from: string;
  to: string;
}

/** The range of a preset on a given day. `custom` keeps whatever range the reader typed. */
export function presetRange(preset: Preset, now: Date, custom: Range = { from: "", to: "" }): Range {
  const y = now.getFullYear();
  const m = now.getMonth();
  switch (preset) {
    case "thisMonth":
      return { from: ymd(new Date(y, m, 1)), to: ymd(new Date(y, m + 1, 0)) };
    case "lastMonth":
      return { from: ymd(new Date(y, m - 1, 1)), to: ymd(new Date(y, m, 0)) };
    case "last30":
      return { from: ymd(new Date(y, now.getMonth(), now.getDate() - 29)), to: ymd(now) };
    case "thisYear":
      return { from: ymd(new Date(y, 0, 1)), to: ymd(now) };
    default:
      return custom;
  }
}

/** Whether a custom range can be sent: both days given, in order, and not longer than the five years the API allows. */
export function validRange(r: Range): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(r.from) || !/^\d{4}-\d{2}-\d{2}$/.test(r.to) || r.from > r.to) return false;
  const limit = new Date(`${r.from}T00:00:00Z`);
  limit.setUTCFullYear(limit.getUTCFullYear() + 5);
  return new Date(`${r.to}T00:00:00Z`) <= limit;
}

const DAILY_BUCKETS_UP_TO_DAYS = 62;

/** Day buckets up to two months read better by day; a longer period is by month (the API's default too). */
export function granularityOf(r: Range): "day" | "month" {
  const days = (new Date(`${r.to}T00:00:00Z`).getTime() - new Date(`${r.from}T00:00:00Z`).getTime()) / 86_400_000 + 1;
  return days <= DAILY_BUCKETS_UP_TO_DAYS ? "day" : "month";
}
