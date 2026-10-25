import { useRouter, useSearch } from "@tanstack/vue-router";
import { safeNext } from "./lib";

/**
 * Where a person goes once they have signed in: the page they were sent away from (`?next=`, a path of this console only) or their home. A page that needs
 * a signed-in person puts the address it was opened at in `next`, so a link to /app/partner/apply?tenant=... works for somebody who is signed out.
 */
export function useAfterSignIn(): () => void {
  const router = useRouter();
  const search = useSearch({ strict: false });
  return () => {
    const next = safeNext((search.value as { next?: unknown }).next);
    if (next) router.history.push(next);
    else void router.navigate({ to: "/app" });
  };
}
