import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE, POLL } from "@/shared/api/limits";

export const webhookKeys = {
  endpoints: (tenantId: string) => ["tenants", tenantId, "webhook-endpoints"] as const,
  /** Every delivery list of the tenant (a prefix of `deliveries`). */
  allDeliveries: (tenantId: string) => ["tenants", tenantId, "webhook-deliveries"] as const,
  deliveries: (tenantId: string, endpointId: string) => ["tenants", tenantId, "webhook-deliveries", endpointId] as const,
};

/** The event types an endpoint can ask for, in the order the documentation gives them. */
export const EVENT_TYPES = [
  "invoice.finalized",
  "invoice.paid",
  "invoice.voided",
  "invoice.uncollectible",
  "credit_note.created",
  "credit_note.refunded",
  "credit_note.refund_failed",
  "subscription.created",
  "subscription.changed",
  "subscription.change_scheduled",
  "subscription.cancelled",
  "subscription.cancellation_scheduled",
  "subscription.cancellation_withdrawn",
  "subscription.paused",
  "subscription.resumed",
  "subscription.moved_to_provider",
  "subscription.trial_will_end",
  "payment_link.created",
  "payment_link.paid",
  "payment_link.expired",
  "credits.purchased",
  "credits.low",
] as const;

export function useWebhookEndpoints(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => webhookKeys.endpoints(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/webhook-endpoints")),
  });
}

export function useWebhookDeliveries(tenantId: MaybeRefOrGetter<string>, endpointId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => webhookKeys.deliveries(toValue(tenantId), toValue(endpointId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/webhook-deliveries", { params: { query: { endpoint_id: toValue(endpointId), page_size: PAGE.history } } })),
    enabled: computed(() => toValue(endpointId) !== ""),
    refetchInterval: POLL.pending,
  });
}

export type EventType = (typeof EVENT_TYPES)[number];

export const isEventType = (v: string): v is EventType => (EVENT_TYPES as readonly string[]).includes(v);

export interface EndpointInput {
  url: string;
  description: string;
  event_types: EventType[];
}

/** Every action on an endpoint or a delivery; each refreshes the lists. */
export function useWebhookActions(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const client = () => tenantApi(toValue(tenantId));
  // Only the webhook lists change with these actions, not the tenant's customers, invoices and so on.
  const refresh = () =>
    Promise.all([queries.invalidateQueries({ queryKey: webhookKeys.endpoints(toValue(tenantId)) }), queries.invalidateQueries({ queryKey: webhookKeys.allDeliveries(toValue(tenantId)) })]);
  const path = (id: string) => ({ params: { path: { id } } });
  return {
    create: useMutation({ mutationFn: (body: EndpointInput) => unwrap(client().POST("/v1/webhook-endpoints", { body })), onSuccess: refresh }),
    update: useMutation({
      mutationFn: (v: EndpointInput & { id: string; active: boolean }) => unwrap(client().PUT("/v1/webhook-endpoints/{id}", { ...path(v.id), body: { url: v.url, description: v.description, event_types: v.event_types, active: v.active } })),
      onSuccess: refresh,
    }),
    remove: useMutation({ mutationFn: (id: string) => unwrap(client().DELETE("/v1/webhook-endpoints/{id}", path(id))), onSuccess: refresh }),
    rotate: useMutation({ mutationFn: (id: string) => unwrap(client().POST("/v1/webhook-endpoints/{id}/rotate-secret", path(id))), onSuccess: refresh }),
    test: useMutation({ mutationFn: (id: string) => unwrap(client().POST("/v1/webhook-endpoints/{id}/test", path(id))), onSuccess: refresh }),
    redeliver: useMutation({ mutationFn: (id: string) => unwrap(client().POST("/v1/webhook-deliveries/{id}/redeliver", path(id))), onSuccess: refresh }),
  };
}
