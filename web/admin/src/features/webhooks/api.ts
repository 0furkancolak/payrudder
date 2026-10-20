import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";

export const webhookKeys = {
  endpoints: (tenantId: string) => ["tenants", tenantId, "webhook-endpoints"] as const,
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
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/webhook-deliveries", { params: { query: { endpoint_id: toValue(endpointId), page_size: 50 } } })),
    enabled: computed(() => toValue(endpointId) !== ""),
    refetchInterval: 5000,
  });
}

export interface EndpointInput {
  url: string;
  description: string;
  event_types: string[];
}

/** Every action on an endpoint or a delivery; each refreshes the lists. */
export function useWebhookActions(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const t = () => tenantApi(toValue(tenantId));
  const refresh = () => queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId)] });
  const path = (id: string) => ({ params: { path: { id } } });
  return {
    create: useMutation({ mutationFn: (body: EndpointInput) => unwrap(t().POST("/v1/webhook-endpoints", { body: body as never })), onSuccess: refresh }),
    update: useMutation({
      mutationFn: (v: EndpointInput & { id: string; active: boolean }) => unwrap(t().PUT("/v1/webhook-endpoints/{id}", { ...path(v.id), body: v as never })),
      onSuccess: refresh,
    }),
    remove: useMutation({ mutationFn: (id: string) => unwrap(t().DELETE("/v1/webhook-endpoints/{id}", path(id))), onSuccess: refresh }),
    rotate: useMutation({ mutationFn: (id: string) => unwrap(t().POST("/v1/webhook-endpoints/{id}/rotate-secret", path(id))), onSuccess: refresh }),
    test: useMutation({ mutationFn: (id: string) => unwrap(t().POST("/v1/webhook-endpoints/{id}/test", path(id))), onSuccess: refresh }),
    redeliver: useMutation({ mutationFn: (id: string) => unwrap(t().POST("/v1/webhook-deliveries/{id}/redeliver", path(id))), onSuccess: refresh }),
  };
}
