import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";

/** The categories of Payrudder's outgoing e-mail, in the order the API lists them. */
export const MAIL_CATEGORIES = ["invoice_issued", "payment_receipts", "payment_failed", "usage_alerts", "system"] as const;
export type MailCategory = (typeof MAIL_CATEGORIES)[number];

export const mailKeys = {
  settings: (tenantId: string) => ["tenants", tenantId, "mail-settings"] as const,
  suppressions: (tenantId: string) => ["tenants", tenantId, "mail-suppressions"] as const,
  messages: (tenantId: string) => ["tenants", tenantId, "mail-messages"] as const,
};

export type MailSuppression = components["schemas"]["MailSuppression"];
export type MailMessage = components["schemas"]["MailMessage"];

export function useMailSettings(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => mailKeys.settings(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/mail-settings")),
    // No tenant yet (the sheet is closed, or the app's tenant is not resolved): a call without X-Payrudder-Tenant would be refused with a 401.
    enabled: computed(() => toValue(tenantId) !== ""),
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

const PAGE = 50;

/** The addresses the tenant's mail is not sent to, in address order, with "load more" through the page token. */
export function useMailSuppressions(tenantId: MaybeRefOrGetter<string>) {
  return useInfiniteQuery({
    queryKey: computed(() => mailKeys.suppressions(toValue(tenantId))),
    enabled: computed(() => toValue(tenantId) !== ""),
    initialPageParam: "",
    queryFn: ({ pageParam }) => unwrap(tenantApi(toValue(tenantId)).GET("/v1/mail-suppressions", { params: { query: { page_size: PAGE, page_token: pageParam || undefined } } })),
    getNextPageParam: (last) => last.next_page_token || undefined,
    retry: false, // a deployment that is not connected to the notifier answers 503 every time
  });
}

/** Mails an address again. */
export function useRemoveMailSuppression(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (address: string) => unwrap(tenantApi(toValue(tenantId)).DELETE("/v1/mail-suppressions/{address}", { params: { path: { address } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: mailKeys.suppressions(toValue(tenantId)) }),
  });
}

/** The tenant's recent mail, newest first, with "load more" through the page token. */
export function useMailMessages(tenantId: MaybeRefOrGetter<string>) {
  return useInfiniteQuery({
    queryKey: computed(() => mailKeys.messages(toValue(tenantId))),
    enabled: computed(() => toValue(tenantId) !== ""),
    initialPageParam: "",
    queryFn: ({ pageParam }) => unwrap(tenantApi(toValue(tenantId)).GET("/v1/mail-messages", { params: { query: { page_size: PAGE, page_token: pageParam || undefined } } })),
    getNextPageParam: (last) => last.next_page_token || undefined,
    retry: false,
  });
}
