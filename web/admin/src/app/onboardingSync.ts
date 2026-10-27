import { setProgressStore } from "@/features/onboarding";
import { loadOnboarding, pushOnboarding } from "@/shared/api/onboarding";
import { setTourStore } from "@/shared/ui/tour";

/**
 * Lets the first-run wizard and the page tours keep their state on the server, per person, instead of in this browser alone: a second browser then does not
 * start them again. Called once at start-up; the two features know nothing of the API, they are only given somewhere to ask and to write.
 */
export function startOnboardingSync(): void {
  setTourStore({
    load: async () => (await loadOnboarding())?.seen_tours ?? null,
    add: (ids) => pushOnboarding({ seen_tours: [...ids] }),
    forget: (ids) => pushOnboarding({ forget_tours: [...ids] }),
  });
  setProgressStore({
    load: async () => {
      const record = await loadOnboarding();
      return record ? { wizard: record.wizard ?? null } : null;
    },
    patch: (change) => pushOnboarding({ wizard: change }),
  });
}
