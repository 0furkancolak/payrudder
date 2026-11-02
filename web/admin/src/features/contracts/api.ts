import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";
import type { Audience, Kind, SettingsDraft } from "./lib";

type Schemas = components["schemas"];

export const contractKeys = {
  versions: (tenantId: string) => ["tenants", tenantId, "contract-versions"] as const,
  settings: (tenantId: string) => ["tenants", tenantId, "contract-settings"] as const,
  withdrawals: (tenantId: string) => ["tenants", tenantId, "contract-withdrawals"] as const,
  acceptances: (tenantId: string, customer: string) => ["tenants", tenantId, "contract-acceptances", customer] as const,
};

// No tenant yet (the page is still resolving its app): a call without X-Payrudder-Tenant would be refused with a 401.
const ready = (tenantId: MaybeRefOrGetter<string>, on: MaybeRefOrGetter<boolean> = true) => computed(() => toValue(tenantId) !== "" && toValue(on));

/** Every version of every series, newest first. */
export function useContractVersions(tenantId: MaybeRefOrGetter<string>, on?: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => contractKeys.versions(toValue(tenantId))),
    queryFn: async () => (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/contracts/versions"))).versions ?? [],
    enabled: ready(tenantId, on),
  });
}

export function useContractSettings(tenantId: MaybeRefOrGetter<string>, on?: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => contractKeys.settings(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/contracts/settings")),
    enabled: ready(tenantId, on),
  });
}

/** Publishes the next version of a series. A first version also makes its kind requirable, so the settings are read again. */
export function usePublishVersion(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: Schemas["PublishContractVersionRequest"]) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/contracts/versions", { body })),
    onSuccess: () => Promise.all([queries.invalidateQueries({ queryKey: contractKeys.versions(toValue(tenantId)) }), queries.invalidateQueries({ queryKey: contractKeys.settings(toValue(tenantId)) })]),
  });
}

/** Renders a text the way a customer sees it (server-escaped HTML with sample values marked) and lists the variables it uses and the ones that do not exist. */
export function usePreviewContract(tenantId: MaybeRefOrGetter<string>) {
  return useMutation({ mutationFn: (v: { body: string; locale: string }) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/contracts/preview", { body: v })) });
}

/** The starter text of a kind in Turkish or English; the API answers plans.contract.no_starter for any other language. */
export function useStarterText(tenantId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: (v: { kind: Kind; audience: Audience; locale: "tr" | "en" }) => unwrap(tenantApi(toValue(tenantId)).GET("/v1/contracts/starter", { params: { query: v } })),
  });
}

export function useSetContractSettings(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: SettingsDraft) => unwrap(tenantApi(toValue(tenantId)).PUT("/v1/contracts/settings", { body })),
    onSuccess: (data) => queries.setQueryData(contractKeys.settings(toValue(tenantId)), data),
  });
}

/** The withdrawal requests of the tenant, newest first, or of one customer. */
export function useContractWithdrawals(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string> = "") {
  return useQuery({
    queryKey: computed(() => [...contractKeys.withdrawals(toValue(tenantId)), toValue(customer)] as const),
    queryFn: async () => {
      const who = toValue(customer);
      const query = who === "" ? undefined : { customer_external_id: who };
      return (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/contract-withdrawals", { params: { query } }))).withdrawals ?? [];
    },
    enabled: ready(tenantId),
  });
}

/** Decides a withdrawal (accepted or rejected, once) or marks an accepted one refunded; the seller refunds, Payrudder does not move the money. */
export function useDecideWithdrawal(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; status: "accepted" | "rejected" | "refunded"; note: string }) =>
      unwrap(tenantApi(toValue(tenantId)).PUT("/v1/contract-withdrawals/{id}/status", { params: { path: { id: v.id } }, body: { status: v.status, note: v.note } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: contractKeys.withdrawals(toValue(tenantId)) }),
  });
}

/** Asks billing to refund an accepted withdrawal (a credit note that returns the amount to the card). The answer says what happened: `refund_state` and the credit note. */
export function useRequestRefund(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/contract-withdrawals/{id}/refund", { params: { path: { id } }, body: {} })),
    onSuccess: () => queries.invalidateQueries({ queryKey: contractKeys.withdrawals(toValue(tenantId)) }),
  });
}

/** Records a statement a customer made outside the pages (a mail, a call) as a withdrawal request, from the console. */
export function useRecordWithdrawal(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { customer: string; statement: string; receiveTime?: string }) =>
      unwrap(
        tenantApi(toValue(tenantId)).POST("/v1/customers/{external_id}/contract-withdrawals", {
          params: { path: { external_id: v.customer } },
          body: { statement: v.statement, source: "console", ...(v.receiveTime ? { receive_time: v.receiveTime } : {}) },
        }),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: contractKeys.withdrawals(toValue(tenantId)) }),
  });
}

/** Everything a customer accepted, one row per version; the log groups them. */
export function useContractAcceptances(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => contractKeys.acceptances(toValue(tenantId), toValue(customer))),
    queryFn: async () => (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers/{external_id}/contract-acceptances", { params: { path: { external_id: toValue(customer) } } }))).acceptances ?? [],
    enabled: ready(tenantId, () => toValue(customer) !== ""),
  });
}

/** The page an acceptance's group accepted, as stored, with its hash (asked for when `id` is set). It is the seller's evidence: show it only in a sandboxed frame. */
export function useContractSnapshot(tenantId: MaybeRefOrGetter<string>, id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "contract-snapshot", toValue(id)] as const),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/contract-acceptances/{id}/snapshot", { params: { path: { id: toValue(id) } } })),
    enabled: ready(tenantId, () => toValue(id) !== ""),
  });
}
