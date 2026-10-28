/** What the person has done so far, read from their data (not from what the wizard remembers), so the checklist is right however the work was done. */
export interface Facts {
  apps: number;
  meters: number;
  packages: number;
  /** Active API keys of the sandbox. */
  keys: number;
  live: boolean;
}

export const ITEMS = ["app", "meter", "package", "key", "live"] as const;
export type Item = (typeof ITEMS)[number];

export interface Row {
  key: Item;
  done: boolean;
}

/** The "Getting started" list, in the order the work goes. */
export function checklist(f: Facts): Row[] {
  const done: Record<Item, boolean> = { app: f.apps > 0, meter: f.meters > 0, package: f.packages > 0, key: f.keys > 0, live: f.live };
  return ITEMS.map((key) => ({ key, done: done[key] }));
}

export const doneCount = (rows: readonly Row[]) => rows.filter((r) => r.done).length;
