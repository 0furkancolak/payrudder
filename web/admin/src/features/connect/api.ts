import { useMutation, useQueryClient } from "@tanstack/vue-query";
import { toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { customerKeys } from "@/features/customers/api";
import { meterKeys } from "@/features/meters/api";
import { SAMPLE_CUSTOMER, SAMPLE_METER, testEvent } from "./sample";

/** Makes the sample meter and/or customer of the sandbox through the tenant API, the meter first (nothing else needs the customer). Both lists are read again. */
export function useCreateSamples(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: async (want: { meter: boolean; customer: boolean }) => {
      const t = tenantApi(toValue(tenantId));
      if (want.meter) await unwrap(t.POST("/v1/meters", { body: SAMPLE_METER }));
      if (want.customer) await unwrap(t.POST("/v1/customers", { body: SAMPLE_CUSTOMER }));
      return want;
    },
    // Whatever was made before a failure is read too.
    onSettled: () =>
      Promise.all([queries.invalidateQueries({ queryKey: meterKeys.list(toValue(tenantId)) }), queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) })]),
  });
}

/** Sends one usage event as the signed-in person (the gateway lets a session call the tenant API for its own tenants). The answer is the event's result: accepted or rejected with a code. */
export function useSendTestEvent(tenantId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: async (v: { meter: string; customer: string }) => {
      const r = await unwrap(tenantApi(toValue(tenantId)).POST("/v1/events", { body: { events: [testEvent(v.meter, v.customer, new Date())] } }));
      const first = r.results?.[0];
      if (!first) throw new Error("the API answered no result for the event");
      return first;
    },
  });
}
