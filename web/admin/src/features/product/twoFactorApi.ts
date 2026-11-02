import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed } from "vue";
import { api, unwrap } from "@/shared/api/client";
import { personToken } from "@/shared/lib/session";
import { productKeys, startSession } from "./api";

// Two-factor sign-in (plan 22, G9). The status is a query; everything that carries a secret (the setup, the recovery codes) is a mutation with `gcTime: 0`
// that the dialog resets when it closes, so the secret is held by the dialog alone and by no cache beyond it.

export const twoFactorKeys = { status: ["auth", "2fa"] as const };

export function useTwoFactorStatus() {
  return useQuery({
    queryKey: twoFactorKeys.status,
    queryFn: () => unwrap(api.GET("/auth/2fa")),
    enabled: computed(() => personToken.value !== null),
  });
}

/** What changed for the person and the pages that read it: the status, and `/auth/me` (its two_factor_* flags). */
function useRefresh() {
  const queries = useQueryClient();
  return () => Promise.all([queries.invalidateQueries({ queryKey: twoFactorKeys.status }), queries.invalidateQueries({ queryKey: productKeys.me })]);
}

/** The second step of a sign-in: the challenge and a six-digit code (or a recovery code) open the session, like any other sign-in. */
export function useVerifyTwoFactor() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { challenge: string; code: string }) => unwrap(api.POST("/auth/2fa/verify", { body: v })),
    onSuccess: (s) => startSession(queries, s),
  });
}

/** A new secret and the address for the QR code. Two-factor is not on until `confirm` has proven a code. */
export function useSetupTwoFactor() {
  return useMutation({ gcTime: 0, mutationFn: () => unwrap(api.POST("/auth/2fa/setup")) });
}

/** Turns two-factor on with the first code of the app; the answer is the ten recovery codes, once. */
export function useConfirmTwoFactor() {
  const refresh = useRefresh();
  return useMutation({
    gcTime: 0,
    mutationFn: (code: string) => unwrap(api.POST("/auth/2fa/confirm", { body: { code } })),
    onSuccess: refresh,
  });
}

/** Turns it off; needs a current code or a recovery code. */
export function useDisableTwoFactor() {
  const refresh = useRefresh();
  return useMutation({
    gcTime: 0,
    mutationFn: (code: string) => unwrap(api.POST("/auth/2fa/disable", { body: { code } })),
    onSuccess: refresh,
  });
}

/** Ten new recovery codes (the old ones stop working); needs a current code or a recovery code. The answer is shown once. */
export function useRegenerateRecoveryCodes() {
  const refresh = useRefresh();
  return useMutation({
    gcTime: 0,
    mutationFn: (code: string) => unwrap(api.POST("/auth/2fa/recovery-codes", { body: { code } })),
    onSuccess: refresh,
  });
}
