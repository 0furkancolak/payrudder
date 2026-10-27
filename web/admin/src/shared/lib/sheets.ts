import { useRouter, useSearch } from "@tanstack/vue-router";
import { computed, nextTick, ref, watch } from "vue";

/**
 * The sheets that are open, as one stack. A sheet that can be reached again by name (a form sheet, or the sheet of a row: `invoice:<id>`) is written
 * into the address as `?sheet=a,b` in the order they were opened, so a reload or a shared link opens the same sheets, and the browser's own history
 * is not filled with them. Every sheet, named or not, is a tab of the strip at the top of the topmost sheet: tabs can be raised to the front or
 * closed, "back" closes the sheet in front and "forward" opens the one closed last.
 */
export interface SheetTab {
  uid: number;
  title: string;
  name?: string;
  close: () => void;
}

const tabs = ref<SheetTab[]>([]);
/** The names open now, as the address holds them. */
export const names = ref<string[]>([]);
/** Names closed by "back", newest last: what "forward" opens again. */
export const ahead = ref<string[]>([]);

let nextUid = 1;
export const newSheetUid = () => nextUid++;

export const openTabs = computed(() => tabs.value);
export const frontUid = computed(() => tabs.value.at(-1)?.uid);
export const depthOf = (uid: number) => tabs.value.findIndex((t) => t.uid === uid);

export function enter(tab: SheetTab) {
  if (!tabs.value.some((t) => t.uid === tab.uid)) tabs.value = [...tabs.value, tab];
}
export function leave(uid: number) {
  tabs.value = tabs.value.filter((t) => t.uid !== uid);
}
export function setTitle(uid: number, title: string) {
  const tab = tabs.value.find((t) => t.uid === uid);
  if (tab && tab.title !== title) tab.title = title;
}
/** Brings an open sheet to the front. */
export function raise(uid: number) {
  const tab = tabs.value.find((t) => t.uid === uid);
  if (tab) tabs.value = [...tabs.value.filter((t) => t !== tab), tab];
}
export function closeFront() {
  tabs.value.at(-1)?.close();
}
/** "Back": closes the sheet in front, remembering its name for "forward". */
export async function back() {
  const tab = tabs.value.at(-1);
  if (!tab) return;
  const before = names.value;
  tab.close();
  await nextTick();
  // The sheet of a row has no name of its own: it is the name that left the address when it closed.
  const gone = before.filter((n) => !names.value.includes(n));
  if (gone.length) ahead.value = [...ahead.value, ...gone];
}
/** "Forward": opens the name closed last by "back". */
export function forward() {
  const name = ahead.value.at(-1);
  if (!name) return;
  ahead.value = ahead.value.slice(0, -1);
  names.value = [...names.value.filter((n) => n !== name), name];
}

export function openName(name: string) {
  if (names.value.includes(name)) return;
  ahead.value = [];
  names.value = [...names.value, name];
}
export function closeName(name: string) {
  if (names.value.includes(name)) names.value = names.value.filter((n) => n !== name);
}

/** The id of a record as a sheet's name holds it: commas (they separate the names in the address) and colons (they separate the parts of a name) are escaped. */
export const sheetKey = (id: string) => encodeURIComponent(id);
/** The name of a sheet that works on one record (`void-expense:<id>`); a sheet shown for several records at once names each by its own. */
export const sheetName = (kind: string, id: string) => `${kind}:${sheetKey(id)}`;

/** The id the open sheet of a kind shows (`invoice:<id>` is the sheet of the kind `invoice` with that id), or undefined when none is open. */
export function idOf(kind: string): string | undefined {
  const prefix = `${kind}:`;
  return names.value.find((n) => n.startsWith(prefix))?.slice(prefix.length);
}
/** Puts the sheet of a kind on a record (or takes it off with null), in the place of the one it showed before so the order of the stack holds. */
export function setKind(kind: string, id: string | null) {
  const prefix = `${kind}:`;
  const at = names.value.findIndex((n) => n.startsWith(prefix));
  const next = [...names.value];
  if (id === null) {
    if (at < 0) return;
    next.splice(at, 1);
  } else if (at >= 0) {
    if (next[at] === prefix + id) return;
    next[at] = prefix + id;
  } else {
    ahead.value = [];
    next.push(prefix + id);
  }
  names.value = next;
}

const parse = (v: unknown): string[] =>
  typeof v === "string" && v !== ""
    ? v.split(",").filter(Boolean)
    : typeof v === "number"
      ? [String(v)]
      : [];
const same = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

/** The address's search parameters as the frame last read them: what the pages keep there besides the sheets (the filters of a list) is read from here. */
export const searchParams = ref<Record<string, unknown>>({});

let routerRef: { navigate: (o: never) => Promise<unknown> } | undefined;
let queue: Promise<unknown> = Promise.resolve();
/**
 * Changes the search of the current page. Changes go one after the other, each seeing the address the one before it left, so two parts of a page that
 * change different parameters at nearly the same moment do not overwrite each other.
 */
export function updateSearch(change: (prev: Record<string, unknown>) => Record<string, unknown>) {
  const router = routerRef;
  if (!router) return;
  queue = queue.then(() => router.navigate({ search: change, replace: true } as never)).catch(() => undefined);
}

/** Keeps `names` and the `sheet` parameter of the address the same thing; called once, by the frame of the pages. */
export function useSheetUrlSync() {
  const router = useRouter();
  routerRef = router as never;
  const search = useSearch({ strict: false });
  const fromUrl = computed(() => parse((search.value as Record<string, unknown>).sheet));
  watch(
    fromUrl,
    (v) => {
      if (!same(v, names.value)) names.value = v;
    },
    { immediate: true },
  );
  watch(search, (v) => (searchParams.value = { ...(v as Record<string, unknown>) }), { immediate: true });
  watch(names, (v) => {
    if (!same(v, fromUrl.value)) updateSearch((prev) => ({ ...prev, sheet: v.length ? v.join(",") : undefined }));
  });
}
