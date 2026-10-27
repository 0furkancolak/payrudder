import { currentToken } from "@/shared/lib/session";
import { api } from "./client";
import type { components } from "./schema";

/**
 * What the console remembers for the signed-in person, kept by the server so a second browser does not start the first-run wizard or a tour again
 * (GET and PATCH /app/v1/me/onboarding). The browser's storage stays a fast cache and the fallback: the features read it first, ask the server once and
 * write every change through. This file is the transport; what the wizard and the tours do with it is theirs (`features/onboarding`, `shared/ui/tour`).
 */
export type OnboardingRecord = components["schemas"]["Onboarding"];
export type WizardDoc = components["schemas"]["OnboardingWizard"];
/** The wizard fields of a change: every one optional, none null. */
export type WizardPatch = components["schemas"]["OnboardingWizardInput"];

/** A change: the wizard fields it names, tours to add to the seen ones and tours to take out of them (a person asking to see a tour again). */
export interface OnboardingPatch {
  wizard?: WizardPatch;
  seen_tours?: string[];
  forget_tours?: string[];
}

/** How long the console waits for the server before it goes on with what the browser remembers. */
const LOAD_TIMEOUT_MS = 8000;

export const patchIsEmpty = (p: OnboardingPatch): boolean => Object.keys(p.wizard ?? {}).length === 0 && !p.seen_tours?.length && !p.forget_tours?.length;

/**
 * The patch that does what `a` and then `b` do. Wizard fields: the later wins. Tours: what `b` adds is no longer forgotten by `a`, and what `b` forgets is
 * no longer added by `a` (the server applies additions first, so a naive union of both lists would get either order wrong).
 */
export function mergePatches(a: OnboardingPatch, b: OnboardingPatch): OnboardingPatch {
  const wizard = { ...a.wizard, ...b.wizard };
  const bSeen = b.seen_tours ?? [];
  const bForget = b.forget_tours ?? [];
  const seen = [...new Set([...(a.seen_tours ?? []), ...bSeen])].filter((id) => !bForget.includes(id));
  const forget = [...new Set([...(a.forget_tours ?? []).filter((id) => !bSeen.includes(id)), ...bForget])];
  return { ...(Object.keys(wizard).length ? { wizard } : {}), ...(seen.length ? { seen_tours: seen } : {}), ...(forget.length ? { forget_tours: forget } : {}) };
}

export interface Writer {
  /** Queues a change for `owner` (the session it was made under). */
  push(patch: OnboardingPatch, owner: string): void;
  /** Resolves when everything queued so far has been sent, or failed. */
  flush(): Promise<void>;
}

/**
 * Write-through that never loses a change and never blocks the page. Changes made while a request is on its way are joined into the next one, requests go
 * one at a time and in order, and one that fails is kept (joined with what came after it) and tried again with the next change or after `retryMs`. A change
 * made under another session than the one now in use is dropped: it belongs to a person who has signed out.
 */
export function createWriter(send: (patch: OnboardingPatch) => Promise<unknown>, whoNow: () => string, retryMs = 15_000): Writer {
  let pending: OnboardingPatch = {};
  let owner = "";
  let chain: Promise<void> = Promise.resolve();
  let timer: ReturnType<typeof setTimeout> | undefined;

  async function drain(): Promise<void> {
    while (!patchIsEmpty(pending)) {
      if (owner !== whoNow()) {
        pending = {};
        return;
      }
      const batch = pending;
      pending = {};
      try {
        await send(batch);
      } catch {
        pending = mergePatches(batch, pending);
        if (retryMs > 0 && timer === undefined) {
          timer = setTimeout(() => {
            timer = undefined;
            chain = chain.then(drain);
          }, retryMs);
        }
        return;
      }
    }
  }

  return {
    push(patch, who) {
      if (who !== owner) pending = {};
      owner = who;
      pending = mergePatches(pending, patch);
      chain = chain.then(drain);
    },
    flush: () => chain,
  };
}

let loaded: { token: string; record: Promise<OnboardingRecord | null> } | undefined;

/**
 * The person's record, asked once per session however many features want it. null when the server cannot say (offline, an older server, no session): the
 * features then go on with their browser's copy. A failure is not remembered, so the next ask tries again.
 */
export function loadOnboarding(): Promise<OnboardingRecord | null> {
  const token = currentToken() ?? "";
  if (!token) return Promise.resolve(null);
  if (loaded?.token === token) return loaded.record;
  const record = api
    .GET("/app/v1/me/onboarding", { signal: AbortSignal.timeout(LOAD_TIMEOUT_MS) })
    .then(({ data }) => data ?? null)
    .catch(() => null);
  loaded = { token, record };
  void record.then((r) => {
    if (r === null && loaded?.record === record) loaded = undefined;
  });
  return record;
}

const writer = createWriter(
  async (patch) => {
    const { error } = await api.PATCH("/app/v1/me/onboarding", { body: patch });
    if (error) throw new Error("onboarding record not saved");
  },
  () => currentToken() ?? "",
);

/** Saves a change for the signed-in person in the background. */
export function pushOnboarding(patch: OnboardingPatch): void {
  const owner = currentToken() ?? "";
  if (owner && !patchIsEmpty(patch)) writer.push(patch, owner);
}
