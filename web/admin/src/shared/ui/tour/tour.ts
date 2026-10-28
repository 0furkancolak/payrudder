import { computed, ref, shallowRef } from "vue";
import { safeGet, safeKeys, safeSet } from "@/shared/lib/storage";

/**
 * The tour engine: a tour is a list of steps, each a message (the i18n keys of its title and body) and, usually, the element of the real page it is about.
 * One tour runs at a time. Which tours a person has seen is remembered per person (the scope is their id, set once by the app frame) and per tour. The
 * server keeps the list (a `TourStore`, registered by the app), so another browser does not repeat a tour; this browser's storage is a fast copy of it and
 * all there is when the server cannot be reached or no store is registered. A blocked storage just means the tour may come again.
 */
export interface TourStep {
  id: string;
  /** A CSS selector of the element to light up. Without one (or when nothing visible matches) the step is a card in the middle of the page. */
  target?: string;
  /** i18n keys, resolved by the host. */
  title: string;
  body: string;
  /** A step about something this screen may not show (the sidebar on a phone): skipped when its target is not there. */
  optional?: boolean;
}

export interface Tour {
  /** Stable: it is the key of "seen". */
  id: string;
  steps: readonly TourStep[];
}

/** A step as a page writes it: its id, the element it is about, and optionally the key of a title shared with other pages (`tour.common.*`). */
export interface StepSpec {
  id: string;
  target?: string;
  optional?: boolean;
  title?: string;
  /** The key of a body shared with other pages (`tour.common.*`). */
  body?: string;
}

/**
 * A tour whose steps are the page's own: `tour.pages.<id>.<step>` is the body of a step and `tour.pages.<id>.<step>Title` its title, unless the spec names
 * a shared one (likewise the body). The first step's title is the page's name (`titleKey`).
 */
export function pageTour(id: string, titleKey: string, steps: readonly StepSpec[]): Tour {
  return {
    id,
    steps: steps.map((s, i) => ({
      id: s.id,
      ...(s.target ? { target: s.target } : {}),
      ...(s.optional ? { optional: true } : {}),
      title: i === 0 ? titleKey : (s.title ?? `tour.pages.${id}.${s.id}Title`),
      body: s.body ?? `tour.pages.${id}.${s.id}`,
    })),
  };
}

/** The step every list page shares: the search box and the filters above the table. */
export const FIND_STEP: StepSpec = { id: "find", target: 'main [role="search"]', title: "tour.common.find", body: "tour.common.findBody" };

/**
 * A switch for the welcome as a whole (the wizard that opens by itself and the tour that starts by itself): `payrudder.noWelcome` = "1" in this browser's
 * storage turns both off. For a support session, and for tests of the real stack that sign fresh people up and want the pages, not the welcome.
 */
export const NO_WELCOME_KEY = "payrudder.noWelcome";
export const welcomeOff = (): boolean => safeGet("local", NO_WELCOME_KEY) === "1";

const KEY = "payrudder.tour.";
/** Whose "seen" list is read and written: the signed-in person's id. Empty (nobody known yet) means no tour starts by itself. */
const scope = ref("");
/** Bumped when a tour is marked seen, so what depends on it re-reads. */
const seenTick = ref(0);
/** The person is known but the server has not yet said which tours they have seen (so another browser's tours are not repeated while it is asked). */
const loading = ref(false);
let loadSeq = 0;

/** Where the seen tours are kept besides this browser. All three fail quietly: the browser's copy is then what there is. */
export interface TourStore {
  /** The seen tours, or null when the server cannot say. */
  load(): Promise<readonly string[] | null>;
  add(ids: readonly string[]): void;
  forget(ids: readonly string[]): void;
}
let store: TourStore | null = null;

/** Registers the server-backed store (null: the browser alone, which is how the engine starts and how its tests run). */
export function setTourStore(next: TourStore | null): void {
  store = next;
}

const seenKey = (id: string, who = scope.value) => `${KEY}${who}.${id}`;

/** The ids this browser has marked seen for a person. */
export function localSeen(who: string): string[] {
  const prefix = seenKey("", who);
  return safeKeys("local", prefix)
    .filter((k) => safeGet("local", k) === "1")
    .map((k) => k.slice(prefix.length));
}

/**
 * Sets whose tours these are (the signed-in person; "" for nobody) and brings the browser's copy together with the server's: what the server says is
 * seen becomes seen here, and what only this browser knows (seen before the server kept them) is sent up. A tour does not start by itself until the server
 * has answered (or has failed to, for a few seconds at most).
 */
export function setTourScope(id: string): void {
  scope.value = id;
  seenTick.value++;
  const mine = ++loadSeq;
  loading.value = false;
  if (!id || !store) return;
  loading.value = true;
  store
    .load()
    .then((server) => {
      if (mine !== loadSeq || !server) return;
      for (const t of server) safeSet("local", seenKey(t, id), "1");
      const ours = localSeen(id).filter((t) => !server.includes(t));
      if (ours.length) store?.add(ours);
    })
    .catch(() => undefined)
    .finally(() => {
      if (mine !== loadSeq) return;
      loading.value = false;
      seenTick.value++;
    });
}

/** Whether the person is known and their seen tours are settled: only then may a tour start by itself. */
export const tourReady = computed(() => !!scope.value && !loading.value);

export function tourSeen(id: string): boolean {
  void seenTick.value;
  return safeGet("local", seenKey(id)) === "1";
}

export function markTourSeen(id: string): void {
  if (scope.value) {
    safeSet("local", seenKey(id), "1");
    store?.add([id]);
  }
  seenTick.value++;
}

export function forgetTours(ids: readonly string[]): void {
  for (const id of ids) safeSet("local", seenKey(id), null);
  if (scope.value && ids.length) store?.forget(ids);
  seenTick.value++;
}

interface Running {
  tour: Tour;
  index: number;
  /** Which way the person is travelling, so a step that is skipped passes on in the same direction. */
  dir: 1 | -1;
}

const running = shallowRef<Running | null>(null);

export const activeTour = computed(() => running.value);
export const activeStep = computed(() => (running.value ? (running.value.tour.steps[running.value.index] ?? null) : null));

export function startTour(tour: Tour): void {
  if (tour.steps.length === 0) return;
  running.value = { tour, index: 0, dir: 1 };
}

/**
 * Starts the tour of a page the first time the person is on it. Returns whether it started. A tour that starts by itself counts as seen from that moment:
 * one that is walked away from (a click on the page, another page) must not come back at the next visit; the tour button starts it again on purpose.
 */
export function startTourOnce(tour: Tour): boolean {
  if (!tourReady.value || running.value || welcomeOff() || tourSeen(tour.id)) return false;
  markTourSeen(tour.id);
  startTour(tour);
  return true;
}

/** Ends the running tour. `remember` (the default) marks it seen, so it does not start by itself again. */
export function endTour(remember = true): void {
  const r = running.value;
  running.value = null;
  if (r && remember) markTourSeen(r.tour.id);
}

export function nextStep(): void {
  const r = running.value;
  if (!r) return;
  if (r.index >= r.tour.steps.length - 1) return endTour();
  running.value = { ...r, index: r.index + 1, dir: 1 };
}

export function prevStep(): void {
  const r = running.value;
  if (!r || r.index === 0) return;
  running.value = { ...r, index: r.index - 1, dir: -1 };
}

/** A step that cannot be shown (its target is not on this screen) is passed over, in the direction of travel. */
export function skipStep(): void {
  const r = running.value;
  if (!r) return;
  // Backwards past the first step there is nowhere to go: the tour goes on from there.
  if (r.dir === 1 || r.index === 0) nextStep();
  else prevStep();
}

/** The element a step is about, if it is on the page and shown. */
export function resolveTarget(selector: string | undefined, root: Pick<Document, "querySelectorAll"> = document): HTMLElement | null {
  if (!selector) return null;
  let found: NodeListOf<Element>;
  try {
    found = root.querySelectorAll(selector);
  } catch {
    return null;
  }
  for (const el of found) {
    if (el instanceof HTMLElement && el.getClientRects().length > 0) return el;
  }
  return null;
}

export interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

/**
 * Where the card goes: under the lit element when it fits, else above it, else beside it (the sidebar is tall, so its card goes to its side), else in the
 * middle. The card never leaves the screen. Pure, so it is tested without a browser.
 */
export function placeCard(target: Box | null, card: { w: number; h: number }, view: { w: number; h: number }, gap = 14, margin = 12): { x: number; y: number; side: "below" | "above" | "start" | "end" | "center" } {
  const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(v, Math.max(lo, hi)));
  const centre = { x: clamp((view.w - card.w) / 2, margin, view.w - card.w - margin), y: clamp((view.h - card.h) / 2, margin, view.h - card.h - margin), side: "center" as const };
  if (!target) return centre;
  const alignX = clamp(target.x + target.w / 2 - card.w / 2, margin, view.w - card.w - margin);
  const alignY = clamp(target.y + target.h / 2 - card.h / 2, margin, view.h - card.h - margin);
  if (target.y + target.h + gap + card.h <= view.h - margin) return { x: alignX, y: target.y + target.h + gap, side: "below" };
  if (target.y - gap - card.h >= margin) return { x: alignX, y: target.y - gap - card.h, side: "above" };
  if (target.x + target.w + gap + card.w <= view.w - margin) return { x: target.x + target.w + gap, y: alignY, side: "end" };
  if (target.x - gap - card.w >= margin) return { x: target.x - gap - card.w, y: alignY, side: "start" };
  return centre;
}
