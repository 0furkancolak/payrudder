import { useNavigate } from "@tanstack/vue-router";
import { useSignOut } from "./api";

/** Signs the person out and takes them to the sign-in page (the user menu and the settings page do exactly this). */
export function useLeave() {
  const signOut = useSignOut();
  const navigate = useNavigate();
  return {
    pending: signOut.isPending,
    async leave() {
      await signOut.mutateAsync();
      await navigate({ to: "/signin", search: { app: undefined } as never });
    },
  };
}
