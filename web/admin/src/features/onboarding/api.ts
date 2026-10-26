import { useMutation, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { useMeters } from "@/features/meters";
import { usePackages } from "@/features/packages";
import { useApiKeys, type App } from "@/features/product/api";
import { ApiError, tenantApi, unwrap } from "@/shared/api/client";
import { liveState, tenantOf } from "@/shared/lib/environment";
import { progress } from "./state";
import { TEST_CUSTOMER } from "./templates";
import type { Facts } from "./checklist";

/** What the "Getting started" list reads: the sandbox of the first app (its meters and packages), its keys, and whether it is live. Nothing is asked for before there is an app. */
export function useSetupFacts(app: MaybeRefOrGetter<App | undefined>) {
  const tenant = computed(() => tenantOf(toValue(app), "sandbox"));
  const appId = computed(() => toValue(app)?.id ?? "");
  const meters = useMeters(tenant);
  const packages = usePackages(tenant);
  const keys = useApiKeys(appId);
  const facts = computed<Facts>(() => ({
    apps: toValue(app) ? 1 : 0,
    meters: meters.data.value?.length ?? 0,
    packages: (packages.data.value ?? []).filter((p) => p.version).length,
    event: progress.value?.event === true,
    keys: (keys.data.value?.api_keys ?? []).filter((k) => k.environment === "ENVIRONMENT_SANDBOX" && k.status === "STATUS_ACTIVE").length,
    live: liveState(toValue(app)) === "active",
  }));
  return { facts, tenant, loading: computed(() => meters.isPending.value || packages.isPending.value || keys.isPending.value) };
}

/** A call that finds the thing already there (a second try after the first got half way) is as good as one that made it. */
async function ensure(call: Promise<{ error?: unknown }>): Promise<void> {
  try {
    await unwrap(call);
  } catch (e) {
    if (!(e instanceof ApiError) || e.code !== "ALREADY_EXISTS") throw e;
  }
}

export interface TestEvent {
  plan: string;
  meter: string;
  quantity: string;
  /** The test customer and its subscription exist already (an earlier try made them). */
  customerReady: boolean;
  /** Called once the customer and its subscription exist, before the event is sent: a try that fails after that does not make them again. */
  onCustomer?: () => void;
}

/**
 * The wizard's last step, in the sandbox: a test customer subscribed to the package the person made, then one usage event for the meter they chose. The
 * answer of the event is read, because the API accepts a request and rejects single events inside it.
 */
export function useSendTestEvent(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: async (v: TestEvent) => {
      const api = tenantApi(toValue(tenantId));
      if (!v.customerReady) {
        await ensure(
          api.POST("/v1/customers", { body: { external_id: TEST_CUSTOMER, name: "Test customer", email: "test.customer@example.com", payment_method: "pm_sandbox_success" } }),
        );
        await unwrap(api.POST("/v1/subscriptions", { body: { customer_external_id: TEST_CUSTOMER, plan_code: v.plan, fee_timing: "FEE_TIMING_ARREARS" } }));
        v.onCustomer?.();
      }
      const sent = await unwrap(
        // A minute in the past: the clock of the person's machine may run ahead of the server's.
        api.POST("/v1/events", { body: { events: [{ customer_external_id: TEST_CUSTOMER, meter_code: v.meter, quantity: v.quantity, event_time: new Date(Date.now() - 60_000).toISOString() }] } }),
      );
      const result = sent.results?.[0];
      if (!result || result.status === "rejected") throw new ApiError(result?.error_message ?? "the event was not accepted", result?.error_code ?? "REJECTED");
      return result;
    },
    onSuccess: () => queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId)] }),
  });
}
