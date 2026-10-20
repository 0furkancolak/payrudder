import { useMutation, useQuery } from "@tanstack/vue-query";
import { computed, ref } from "vue";
import { api, asTenant, unwrap, type Invoice } from "@/shared/api/client";

export const CURRENCIES = ["CHF", "EUR", "GBP", "JPY", "KWD", "TRY", "USD"] as const;
export const SCENARIOS = ["success", "declined", "insufficient_funds_once", "error_then_success", "timeout_committed", "timeout_not_committed"] as const;
export type Currency = (typeof CURRENCIES)[number];
export type Scenario = (typeof SCENARIOS)[number];

const CUSTOMER = "cus_playground";

/** What the walk-through has done so far, kept as message keys so it re-renders when the language changes. */
export type LogLine = { key: string; params?: Record<string, string | number> };

/**
 * The state and the steps of the playground: an account, then what it meters and charges, usage, the period close, and the invoice
 * followed through its saga. Everything runs against the public API with the sandbox tenant's own key.
 */
export function usePlayground() {
  const tenantId = ref<string | null>(null);
  const key = ref<string | null>(null);
  const ready = ref(false);
  const scenario = ref<Scenario>("success");
  const sent = ref(0);
  const closed = ref(false);
  const log = ref<LogLine[]>([]);
  const note = (line: LogLine) => log.value.push(line);

  const createAccount = useMutation({
    mutationFn: async (input: { name: string; currency: Currency; timezone: string; graceHours: number }) => {
      const account = await unwrap(
        api.POST("/admin/v1/accounts", {
          body: { name: input.name, currency: input.currency, timezone: input.timezone, grace_period: `${input.graceHours * 3600}s` },
        }),
      );
      note({ key: "playground.log.account" });
      const sandbox = account.tenants?.find((t) => t.environment === "ENVIRONMENT_SANDBOX");
      if (!sandbox?.id) throw new Error("the account has no sandbox tenant");
      const issued = await unwrap(api.POST("/admin/v1/tenants/{id}/api-keys", { params: { path: { id: sandbox.id } } }));
      if (!issued.plaintext) throw new Error("no API key was issued");
      note({ key: "playground.log.key" });
      return { tenant: sandbox.id, key: issued.plaintext };
    },
    onSuccess: (r) => {
      tenantId.value = r.tenant;
      key.value = r.key;
    },
  });

  const setUp = useMutation({
    mutationFn: async () => {
      const t = asTenant(requireKey());
      await unwrap(t.POST("/v1/meters", { body: { code: "api_calls", aggregation: "AGGREGATION_SUM", value_property: "n", quantity_scale: 0, unit_label: "calls" } }));
      note({ key: "playground.log.meter" });
      await unwrap(t.POST("/v1/plans", { body: { code: "starter", name: "Starter" } }));
      note({ key: "playground.log.plan" });
      await unwrap(
        t.POST("/v1/plans/{code}/versions", {
          params: { path: { code: "starter" } },
          body: { base_fee_minor: "1000", prices: [{ meter_code: "api_calls", per_unit: { unit_price: "0.01" } }] },
        }),
      );
      note({ key: "playground.log.version" });
      await unwrap(
        t.POST("/v1/customers", { body: { external_id: CUSTOMER, name: "Playground customer", email: "playground@example.com", payment_method: "pm_sandbox_success" } }),
      );
      note({ key: "playground.log.customer", params: { id: CUSTOMER } });
      await unwrap(t.POST("/v1/subscriptions", { body: { customer_external_id: CUSTOMER, plan_code: "starter", fee_timing: "FEE_TIMING_ARREARS" } }));
      note({ key: "playground.log.subscription" });
    },
    onSuccess: () => {
      ready.value = true;
    },
  });

  const chooseScenario = useMutation({
    mutationFn: async (next: Scenario) => {
      await unwrap(asTenant(requireKey()).POST("/v1/sandbox/customers/{external_id}/payment-scenario", { params: { path: { external_id: CUSTOMER } }, body: { scenario: next } }));
      return next;
    },
    onSuccess: (next) => {
      scenario.value = next;
      note({ key: "playground.log.outcome", params: { scenario: next } });
    },
  });

  const sendUsage = useMutation({
    mutationFn: async (calls: number) => {
      await unwrap(
        asTenant(requireKey()).POST("/v1/events", {
          body: { events: [{ customer_external_id: CUSTOMER, meter_code: "api_calls", quantity: String(calls), event_time: new Date(Date.now() - 60_000).toISOString() }] },
        }),
      );
      return calls;
    },
    onSuccess: (calls) => {
      sent.value += calls;
      note({ key: "playground.log.usage", params: { n: calls } });
    },
  });

  const closePeriod = useMutation({
    mutationFn: () => unwrap(asTenant(requireKey()).POST("/v1/sandbox/close-period", { body: {} })),
    onSuccess: () => {
      closed.value = true;
      note({ key: "playground.log.closed" });
    },
  });

  // After the close, follow the invoice through its saga until it stops moving.
  const invoices = useQuery({
    queryKey: computed(() => ["playground", tenantId.value, "invoices"]),
    queryFn: async (): Promise<Invoice[]> => (await unwrap(asTenant(requireKey()).GET("/v1/invoices", {}))).invoices ?? [],
    enabled: computed(() => closed.value && key.value !== null),
    refetchInterval: 2000,
  });

  function requireKey(): string {
    if (key.value === null) throw new Error("no sandbox key yet");
    return key.value;
  }

  const busy = computed(() => [createAccount, setUp, chooseScenario, sendUsage, closePeriod].some((m) => m.isPending.value));
  const error = computed(() => [createAccount, setUp, chooseScenario, sendUsage, closePeriod].map((m) => m.error.value).find((e) => e != null) ?? null);

  return { tenantId, key, ready, scenario, sent, closed, log, busy, error, createAccount, setUp, chooseScenario, sendUsage, closePeriod, invoices };
}
