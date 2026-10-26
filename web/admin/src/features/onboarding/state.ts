import { ref } from "vue";
import { safeGet, safeSet } from "@/shared/lib/storage";

/** The steps of the first-run wizard, in order. */
export const STEPS = ["welcome", "organisation", "app", "meter", "package", "event", "done"] as const;
export type Step = (typeof STEPS)[number];

/** Where a person is in the wizard, and what it made, so that coming back (another day, another tab) continues instead of starting again. */
export interface Progress {
  /** active: started and not finished, so the wizard opens by itself; skipped and done: it stays closed, and the checklist on the home carries on. */
  status: "active" | "skipped" | "done";
  step: Step;
  /** What the person chose in the organisation step, for the app step to use. */
  currency?: string;
  timezone?: string;
  /** What the wizard made (ids and codes), so a step that already ran is not run again. */
  app?: string;
  meter?: string;
  plan?: string;
  customer?: boolean;
  event?: boolean;
  /** The person closed the "Getting started" card. */
  hidden?: boolean;
}

const KEY = "payrudder.onboarding.";
const scope = ref("");
/** What is stored for the scope's person, or null when nothing is. Reactive: the home reads it. */
export const progress = ref<Progress | null>(null);

function parse(text: string | null): Progress | null {
  if (!text) return null;
  try {
    const v = JSON.parse(text) as Partial<Progress> | null;
    if (!v || (v.status !== "active" && v.status !== "skipped" && v.status !== "done")) return null;
    return { ...v, status: v.status, step: STEPS.includes(v.step as Step) ? (v.step as Step) : "welcome" };
  } catch {
    return null;
  }
}

/** Reads the signed-in person's progress. Called when their id is known. */
export function loadProgress(userId: string): void {
  scope.value = userId;
  progress.value = userId ? parse(safeGet("local", KEY + userId)) : null;
}

function save(next: Progress | null): void {
  progress.value = next;
  if (scope.value) safeSet("local", KEY + scope.value, next ? JSON.stringify(next) : null);
}

/** Changes what is stored; the wizard becomes (or stays) active unless the patch says otherwise. */
export function patchProgress(patch: Partial<Progress>): void {
  save({ status: "active", step: "welcome", ...progress.value, ...patch });
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

/** A package code from a name: lower case, letters and digits, dashes between words, starting with a letter (what the API accepts). */
export function slug(name: string): string {
  const s = name
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    // The dotless i of Turkish has no decomposition.
    .replace(/ı/g, "i")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  if (s === "") return "starter";
  return (/^[a-z]/.test(s) ? s : `p-${s}`).slice(0, 63).replace(/-+$/, "");
}
