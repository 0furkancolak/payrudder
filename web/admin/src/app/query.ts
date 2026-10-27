import { MutationCache, QueryClient } from "@tanstack/vue-query";
import { toast } from "vue-sonner";
import { ApiError } from "@/shared/api/client";
import { isUpgradeError } from "@/features/saas/lib";
import { errorText } from "@/shared/lib/errors";
import { i18n } from "./i18n";

/** A refused create because the plan's limit is reached: say so with a way out (the plan page). The page's own ErrorText still shows the message. */
function offerUpgrade(error: unknown): void {
  if (!isUpgradeError(error)) return;
  const t = i18n.global.t as (key: string, named?: Record<string, unknown>) => string;
  const te = i18n.global.te as (key: string) => boolean;
  toast.error(errorText(t, te, error), {
    action: {
      label: t("saas.upgrade"),
      onClick: () => {
        // Imported when needed: the router imports this file.
        void import("./router").then(({ router }) => router.navigate({ to: "/app/settings/plan" as never })).catch(() => undefined);
      },
    },
  });
}

export const queryClient = new QueryClient({
  mutationCache: new MutationCache({ onError: offerUpgrade }),
  defaultOptions: {
    queries: {
      // A refused token or a missing resource will not get better by asking again.
      retry: (count, error) => !(error instanceof ApiError && ["UNAUTHENTICATED", "PERMISSION_DENIED", "NOT_FOUND", "INVALID_ARGUMENT"].includes(error.code)) && count < 2,
      refetchOnWindowFocus: false,
      staleTime: 5_000,
    },
  },
});
