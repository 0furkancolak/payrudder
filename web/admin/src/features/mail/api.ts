import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";

/** The categories of Quotify's outgoing e-mail, in the order the API lists them. */
export const MAIL_CATEGORIES = ["invoice_issued", "payment_receipts", "payment_failed", "usage_alerts", "system"] as const;
export type MailCategory = (typeof MAIL_CATEGORIES)[number];

export const mailKeys = { settings: (tenantId: string) => ["tenants", tenantId, "mail-settings"] as const };

export function useMailSettings(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => mailKeys.settings(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/mail-settings")),
  });
}

/** Switches one category on or off; the others keep their setting. */
export function useSetMailCategory(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (change: { category: MailCategory; enabled: boolean }) => unwrap(tenantApi(toValue(tenantId)).PUT("/v1/mail-settings", { body: { categories: [change] } })),
    onSuccess: (data) => queries.setQueryData(mailKeys.settings(toValue(tenantId)), data),
  });
}
