import { ref } from "vue";
import { safeGet, safeSet } from "@/shared/lib/storage";

/** The steps of the first-run wizard, in order. */
export const STEPS = ["welcome", "app", "done"] as const;
export type Step = (typeof STEPS)[number];

/**
 * Where a person is in the wizard, and what it made, so that coming back (another day, another browser) continues instead of starting again. The server keeps
 * it per person (a `ProgressStore`, registered by the app); this browser's storage is a fast copy and all there is when the server cannot be reached.
 */
export interface Progress {
  /** active: started and not finished, so the wizard opens by itself; skipped and done: it stays closed, and the checklist on the home carries on. */
  status: "active" | "skipped" | "done";
  step: Step;
  /** What the person chose in the setup step. */
  currency?: string;
  timezone?: string;
  /** The app the wizard made, so coming back to the step does not make another. */
  app?: string;
  /** The person closed the "Getting started" card. */
  hidden?: boolean;
}

const KEY = "payrudder.onboarding.";
const scope = ref("");
/** What is stored for the scope's person, or null when nothing is. Reactive: the home reads it. */
export const progress = ref<Progress | null>(null);
/** False while the server is being asked for the person's progress: until it answers, the wizard must not open (it may be done in another browser). */
export const progressReady = ref(true);

/** Where the progress is kept besides this browser. Both fail quietly: the browser's copy is then what there is. */
export interface ProgressStore {
  /** The server's progress: null inside when the person never started, null outside when the server cannot say. */
  load(): Promise<{ wizard: Partial<Progress> | null } | null>;
  patch(change: Partial<Progress>): void;
}
let store: ProgressStore | null = null;

/** Registers the server-backed store (null: the browser alone, which is how it starts and how its tests run). */
export function setProgressStore(next: ProgressStore | null): void {
  store = next;
}

/** The person whose progress has been brought together with the server's, so that coming back to the home does not undo what was done since. */
let synced = "";
/** The person whose progress is being asked for, and what changed meanwhile. */
let asking = "";
let changedWhileAsking: Partial<Progress> = {};

function parse(text: string | null): Progress | null {
  if (!text) return null;
  try {
    return parseDoc(JSON.parse(text));
  } catch {
    return null;
  }
}

/** The steps of earlier versions (organisation, app) are the one app step now; the ones after it (meter, package, event) are on the checklist. */
function stepOf(stored: string | undefined): Step {
  if (STEPS.includes(stored as Step)) return stored as Step;
  if (stored === "organisation") return "app";
  return stored === "meter" || stored === "package" || stored === "event" ? "done" : "welcome";
}

function parseDoc(v: Partial<Progress> | null | undefined): Progress | null {
  if (!v || (v.status !== "active" && v.status !== "skipped" && v.status !== "done")) return null;
  return { ...v, status: v.status, step: stepOf(v.step) };
}

/**
 * Reads the signed-in person's progress. Called when their id is known. The browser's copy is read at once; the first time for a person in this page, the
 * server's is asked for too: where the server has progress it wins, and what only this browser has (progress made before the server kept it) is sent up.
 */
export function loadProgress(userId: string): void {
  scope.value = userId;
  progress.value = userId ? parse(safeGet("local", KEY + userId)) : null;
  if (!userId || !store || synced === userId || asking === userId) return;
  asking = userId;
  changedWhileAsking = {};
  progressReady.value = false;
  store
    .load()
    .then((server) => {
      if (scope.value !== userId || !server) return;
      synced = userId;
      const ours = progress.value;
      const theirs = parseDoc(server.wizard);
      if (!theirs) {
        if (ours) store?.patch(ours);
        return;
      }
      // Fields only this browser has are kept (and sent up); the rest is the server's; what was changed here while it was asked is newer than both.
      const onlyHere = Object.fromEntries(Object.entries(ours ?? {}).filter(([k]) => theirs[k as keyof Progress] === undefined)) as Partial<Progress>;
      if (Object.keys(onlyHere).length) store?.patch(onlyHere);
      keep({ ...onlyHere, ...theirs, ...changedWhileAsking } as Progress);
    })
    .catch(() => undefined)
    .finally(() => {
      if (asking === userId) {
        asking = "";
        progressReady.value = true;
      }
    });
}

/** Remembers it in this browser and shows it. */
function keep(next: Progress): void {
  progress.value = next;
  if (scope.value) safeSet("local", KEY + scope.value, JSON.stringify(next));
}

function save(next: Progress, change: Partial<Progress>): void {
  keep(next);
  if (!scope.value) return;
  if (asking === scope.value) changedWhileAsking = { ...changedWhileAsking, ...change };
  store?.patch(change);
}

/** Changes what is stored; the wizard becomes (or stays) active unless the patch says otherwise. */
export function patchProgress(patch: Partial<Progress>): void {
  save({ status: "active", step: "welcome", ...progress.value, ...patch }, patch);
}

export const skipOnboarding = () => patchProgress({ status: "skipped" });
export const finishOnboarding = () => patchProgress({ status: "done", step: "done" });
export const resumeOnboarding = () => patchProgress({ status: "active" });
export const hideChecklist = () => patchProgress({ hidden: true });

/**
 * Whether the wizard fills the screen. It opens by itself for somebody who has no app and has not been through it (a new sign-up), keeps opening
 * while it is half done, and never for a viewer (who cannot create anything) or somebody who skipped or finished it.
 */
export function wizardOpen(p: Progress | null, o: { appCount: number | undefined; canCreate: boolean }): boolean {
  if (!o.canCreate || o.appCount === undefined) return false;
  if (p) return p.status === "active";
  return o.appCount === 0;
}

/** The step after or before another, staying inside the list. */
export function stepBy(step: Step, by: 1 | -1): Step {
  const i = STEPS.indexOf(step) + by;
  return STEPS[Math.min(STEPS.length - 1, Math.max(0, i))] ?? "welcome";
}
