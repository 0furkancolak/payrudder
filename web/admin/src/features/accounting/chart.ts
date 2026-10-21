/** Geometry for the two inline-SVG charts. Pure functions: the page only draws what they return, so there is no chart library in the bundle. */

/** One colour per category, chosen to stay apart for the common kinds of colour blindness; every slice also has its name and share in the legend. */
export const CATEGORY_COLORS: Record<string, string> = {
  infrastructure: "#2563eb",
  payment_fees: "#d97706",
  marketing: "#9333ea",
  payroll: "#059669",
  tax: "#dc2626",
  software: "#0891b2",
  other: "#6b7280",
};

export interface CategoryAmount {
  category?: string;
  amount_minor?: string;
}

export interface Slice {
  category: string;
  /** The share of the whole, 0 to 100. */
  percent: number;
  /** stroke-dasharray / stroke-dashoffset for a circle of circumference 100 (r = 100 / 2π), drawn from 12 o'clock clockwise. */
  dash: string;
  offset: number;
  color: string;
}

/** The slices of a donut. Amounts are int64 strings; ratios are all that is needed, so Number is enough. An empty or zero total has no slices. */
export function donutSlices(items: readonly CategoryAmount[]): Slice[] {
  const rows = items.map((i) => ({ category: i.category ?? "other", value: Number(i.amount_minor ?? 0) })).filter((r) => r.value > 0);
  const total = rows.reduce((s, r) => s + r.value, 0);
  if (total <= 0) return [];
  let used = 0;
  return rows.map((r) => {
    const percent = (r.value / total) * 100;
    const slice: Slice = {
      category: r.category,
      percent,
      dash: `${percent} ${100 - percent}`,
      // A dash starts at 3 o'clock; offsetting by 25 moves the start to 12, and each slice begins where the last ended.
      offset: 25 - used,
      color: CATEGORY_COLORS[r.category] ?? CATEGORY_COLORS.other!,
    };
    used += percent;
    return slice;
  });
}

export interface SeriesPoint {
  bucket?: string;
  net_income_minor?: string;
  expenses_minor?: string;
}

export interface Bar {
  bucket: string;
  /** Heights as a share of the tallest bar, 0 to 1. A loss (negative net income) has no income bar. */
  income: number;
  expense: number;
}

/** Heights of the income and expense bars of a series, scaled to the tallest value in it. */
export function barHeights(points: readonly SeriesPoint[]): Bar[] {
  const rows = points.map((p) => ({ bucket: p.bucket ?? "", income: Math.max(0, Number(p.net_income_minor ?? 0)), expense: Math.max(0, Number(p.expenses_minor ?? 0)) }));
  const max = Math.max(0, ...rows.map((r) => Math.max(r.income, r.expense)));
  return rows.map((r) => ({ bucket: r.bucket, income: max > 0 ? r.income / max : 0, expense: max > 0 ? r.expense / max : 0 }));
}
