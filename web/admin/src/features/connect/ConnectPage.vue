<script setup lang="ts">
import { Check, CircleDashed, Copy, KeyRound, Send, Sparkles } from "@lucide/vue";
import { Link } from "@tanstack/vue-router";
import { useQuery } from "@tanstack/vue-query";
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useApiKeys, useApps, useCreateApiKey } from "@/features/product/api";
import { useMeters } from "@/features/meters/api";
import { useCustomers } from "@/features/customers/api";
import { seriesOptions } from "@/features/usage/api";
import { useTenants } from "@/features/tenants/api";
import { answerBody, plainAnswer } from "@/features/webhooks/lib";
import { useWebhookActions, useWebhookDeliveries, useWebhookEndpoints } from "@/features/webhooks/api";
import { useCanWrite } from "@/shared/lib/access";
import { copyText } from "@/shared/lib/clipboard";
import { envOf, findTenant } from "@/shared/lib/environment";
import { firstError } from "@/shared/lib/errors";
import { useRouteTenantId } from "@/shared/lib/routeTenant";
import { isOperator, tenantBase } from "@/shared/lib/session";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { PageHeader } from "@/shared/ui/page-header";
import { SegmentedControl } from "@/shared/ui/segmented";
import { useCreateSamples, useSendTestEvent } from "./api";
import { SAMPLE_CUSTOMER, SAMPLE_METER, missingSamples } from "./sample";
import { initialAddress } from "./address";
import { eventErrorHintKey } from "./eventErrors";
import RejectedEvents from "./RejectedEvents.vue";
import { LANGS, keyHintOf, snippets, type Lang } from "./snippets";

/**
 * Connect an app: the steps from "I have an app" to "my first event arrived", with the code to paste. The code is built from the app's own meter and a customer id,
 * for the environment the page is in (a test key shape for the sandbox, a live one for live). The checklist turns green as the things happen: a meter exists, a
 * customer exists, and a usage event has been counted (asked for every few seconds while the page is open). The API address comes from the deployment's PUBLIC_API_URL setting
 * (/env.js) when it has one, else it is typed once and kept in this browser. Under the webhook code, a button sends the endpoint a test event and shows how the endpoint answered.
 *
 * For a person who owns the app the page can also make the first things itself: an ingest-only API key (shown once, held in memory until the person says they have it,
 * never stored), in the sandbox a sample meter and customer, and a button that sends one test event as the signed-in person. All three use the APIs the console already uses.
 */
const { t } = useI18n();
// The code, the checklist and the address are for everybody; making a key, the samples or a test event writes, so a viewer is not offered those (the server would refuse them).
const canWrite = useCanWrite();
const tenantId = useRouteTenantId();
const apps = useApps();
const operatorTenants = useTenants({ enabled: isOperator });
const live = computed(() => {
  const id = tenantId.value;
  const env = findTenant(apps.data.value?.apps, id)?.tenant.environment ?? operatorTenants.data.value?.tenants?.find((x) => x.id === id)?.environment;
  return env === "ENVIRONMENT_LIVE";
});
/** The app this tenant belongs to, when the signed-in person has it (an operator looking at a tenant does not, and is not offered a key here). */
const appId = computed(() => findTenant(apps.data.value?.apps, tenantId.value)?.app.id ?? "");
const env = computed(() => (live.value ? "live" : "sandbox") as "live" | "sandbox");

const meters = useMeters(tenantId);
const customers = useCustomers(tenantId);
const meter = ref("");
watch(() => meters.data.value, (list) => {
  if (!meter.value || !(list ?? []).some((m) => m.code === meter.value)) meter.value = list?.[0]?.code ?? "";
}, { immediate: true });
const customer = ref("cus_1");

const STORE = "payrudder-api-address";
const readAddress = () => {
  try {
    return localStorage.getItem(STORE) ?? "";
  } catch {
    return "";
  }
};
const baseUrl = ref(initialAddress(readAddress(), window.__PAYRUDDER__?.api));
watch(baseUrl, (v) => {
  try {
    localStorage.setItem(STORE, v);
  } catch {
    /* the address is not kept; it is typed again next time */
  }
});

const lang = ref<Lang>("typescript");
const langOptions = LANGS.map((l) => ({ value: l, label: t(`connect.langs.${l}`) }));
const code = computed(() => snippets(lang.value, { baseUrl: baseUrl.value, meter: meter.value || "your_meter", customer: customer.value.trim() || "cus_1", keyHint: keyHintOf(live.value) }));

// Has a usage event been counted? Any non-zero bucket of the chosen meter in the last week.
const today = () => new Date().toISOString().slice(0, 10);
const weekAgo = () => new Date(Date.now() - 6 * 86_400_000).toISOString().slice(0, 10);
const arrived = useQuery({
  queryKey: computed(() => ["tenants", tenantId.value, "connect-first-event", meter.value] as const),
  queryFn: async () => {
    const s = await seriesOptions(tenantId.value, meter.value, { from: weekAgo(), to: today() }, false).queryFn();
    return s.current.some((b) => b.events > 0 || Number(b.quantity) > 0);
  },
  enabled: computed(() => tenantId.value !== "" && meter.value !== ""),
  refetchInterval: (q) => (q.state.data === true ? false : 5000),
});

// The webhook: the endpoints that exist, the one a test event goes to, and how that test delivery went (looked at every few seconds until it is settled).
const endpoints = useWebhookEndpoints(tenantId);
const hooks = computed(() => endpoints.data.value?.endpoints ?? []);
const hookActions = useWebhookActions(tenantId);
const endpointId = ref("");
watch(hooks, (list) => {
  if (!list.some((e) => e.id === endpointId.value)) endpointId.value = list[0]?.id ?? "";
}, { immediate: true });
const sentId = ref("");
watch(endpointId, () => (sentId.value = ""));
function sendTestWebhook() {
  hookActions.test.mutate(endpointId.value, { onSuccess: (d) => (sentId.value = d.id ?? "") });
}
const deliveries = useWebhookDeliveries(tenantId, () => (sentId.value ? endpointId.value : ""));
const delivery = computed(() => (sentId.value ? deliveries.data.value?.deliveries?.find((d) => d.id === sentId.value) : undefined));
const outcome = computed<"" | "queued" | "delivered" | "failed">(() => {
  if (!sentId.value) return "";
  const d = delivery.value;
  if (d?.state === "WEBHOOK_DELIVERY_STATE_DELIVERED") return "delivered";
  return d && Number(d.attempts) > 0 ? "failed" : "queued";
});
const answer = computed(() => {
  const d = delivery.value;
  const text = plainAnswer(answerBody(d?.last_error, d?.last_status)).text;
  return [Number(d?.last_status) > 0 ? `HTTP ${d?.last_status}` : "", text].filter(Boolean).join(": ") || t("connect.webhookTest.noAnswer");
});
const webhookError = computed(() => firstError(hookActions.test));

// An API key: the ones that exist are listed without their secret; a new one is shown once and lives in this ref until the person has copied it (never in storage, never in the code above).
const keys = useApiKeys(appId);
const hasKey = computed(() => (keys.data.value?.api_keys ?? []).some((k) => envOf(k.environment) === env.value && k.status === "STATUS_ACTIVE"));
const createKey = useCreateApiKey(appId);
const secret = ref("");
const keyCopied = ref(false);
function makeKey() {
  keyCopied.value = false;
  createKey.mutate({ environment: env.value, role: "ingest_only" }, { onSuccess: (r) => (secret.value = r.plaintext ?? "") });
}
async function copySecret() {
  keyCopied.value = await copyText(secret.value);
}
// The secret does not outlive the tenant on screen or the page.
watch(tenantId, () => (secret.value = ""));
onBeforeUnmount(() => (secret.value = ""));

// Sandbox only: the meter and the customer the code starts from, made for whichever of the two is missing.
const missing = computed(() => missingSamples(meters.data.value ?? [], customers.data.value?.customers ?? []));
const showSample = computed(() => !live.value && meters.isSuccess.value && customers.isSuccess.value && (missing.value.meter || missing.value.customer));
const samples = useCreateSamples(tenantId);
function makeSamples() {
  samples.mutate(missing.value, {
    onSuccess: (made) => {
      if (made.customer) customer.value = SAMPLE_CUSTOMER.external_id;
    },
  });
}

// Sandbox only: one event for the chosen meter and customer, sent as the signed-in person. Its result (accepted, or rejected with a code) is shown; the checklist's own poll
// turns "an event has been counted" green when the totals have caught up.
const testEvent = useSendTestEvent(tenantId);
const eventReady = computed(() => meter.value !== "" && customer.value.trim() !== "");
function sendTestEvent() {
  testEvent.mutate({ meter: meter.value, customer: customer.value.trim() });
}
watch([meter, customer], () => testEvent.reset());

const steps = computed(() => [
  { key: "key", done: hasKey.value || secret.value !== "", optional: true },
  { key: "meter", done: (meters.data.value ?? []).length > 0 },
  { key: "customer", done: (customers.data.value?.customers ?? []).length > 0 },
  { key: "event", done: arrived.data.value === true },
  { key: "webhook", done: hooks.value.length > 0 },
]);
const copied = ref("");
async function copy(which: "install" | "send" | "webhook") {
  if (await copyText(code.value[which])) {
    copied.value = which;
    setTimeout(() => (copied.value = ""), 1500);
  }
}
const base = computed(() => tenantBase());
</script>

<template>
  <PageHeader :title="t('connect.title')" :description="t('connect.intro')" />

  <Card class="mb-4 p-5" data-testid="connect-checklist">
    <h2 class="mb-3 text-base font-semibold">{{ t("connect.checklist") }}</h2>
    <ol class="grid gap-2 text-sm">
      <li v-for="s in steps" :key="s.key" class="flex items-start gap-3" :data-testid="`connect-step-${s.key}`" :data-done="s.done ? 'true' : 'false'">
        <Check v-if="s.done" class="text-success mt-0.5 size-4 shrink-0" aria-hidden="true" />
        <CircleDashed v-else class="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />
        <span>
          <span class="font-medium">{{ t(`connect.steps.${s.key}.title`) }}</span>
          <span class="text-muted-foreground block text-xs">{{ s.done ? t(`connect.steps.${s.key}.done`) : t(`connect.steps.${s.key}.todo`) }}</span>
          <Link v-if="!s.done && s.key === 'meter'" :to="`${base}/${tenantId}/meters`" class="text-primary text-xs hover:underline">{{ t("connect.steps.meter.link") }}</Link>
          <Link v-if="!s.done && s.key === 'webhook'" :to="`${base}/${tenantId}/webhooks`" class="text-primary text-xs hover:underline">{{ t("connect.steps.webhook.link") }}</Link>
        </span>
      </li>
    </ol>
    <div v-if="showSample && canWrite" class="mt-3 grid gap-2" data-testid="connect-sample">
      <div><Button type="button" variant="outline" size="sm" :disabled="samples.isPending.value" data-testid="connect-sample-create" @click="makeSamples"><Sparkles class="size-4" aria-hidden="true" />{{ t("connect.sample.create") }}</Button></div>
      <p class="text-muted-foreground text-xs">{{ t("connect.sample.help", { meter: SAMPLE_METER.code, customer: SAMPLE_CUSTOMER.external_id }) }}</p>
    </div>
    <ErrorText :error="firstError(samples)" />
    <p v-if="!arrived.data.value" class="text-muted-foreground mt-3 text-xs" role="status" data-testid="connect-waiting">{{ t("connect.waiting") }}</p>
  </Card>

  <Card class="mb-4 p-5">
    <h2 class="mb-1 text-base font-semibold">{{ t("connect.codeTitle") }}</h2>
    <p class="text-muted-foreground mb-3 text-sm">{{ t(live ? "connect.keyLive" : "connect.keyTest") }}</p>
    <div v-if="appId && canWrite" class="bg-muted/40 mb-4 grid gap-3 rounded-2xl p-4" data-testid="connect-key">
      <h3 class="text-sm font-semibold">{{ t("connect.key.title") }}</h3>
      <template v-if="!secret">
        <p class="text-muted-foreground text-xs">{{ t("connect.key.help") }}</p>
        <div><Button type="button" variant="outline" :disabled="createKey.isPending.value" data-testid="connect-key-create" @click="makeKey"><KeyRound class="size-4" aria-hidden="true" />{{ t("connect.key.create") }}</Button></div>
      </template>
      <template v-else>
        <p class="text-xs font-medium" role="status">{{ t("connect.key.once") }}</p>
        <div class="flex flex-wrap items-center gap-2">
          <code class="bg-background mono min-w-0 flex-1 overflow-x-auto rounded-xl px-3 py-2 text-xs" data-testid="connect-key-secret">{{ secret }}</code>
          <Button type="button" variant="ghost" size="sm" data-testid="connect-key-copy" @click="copySecret"><Copy class="size-4" aria-hidden="true" />{{ keyCopied ? t("common.copied") : t("connect.copy") }}</Button>
        </div>
        <div><Button type="button" variant="outline" size="sm" data-testid="connect-key-done" @click="secret = ''">{{ t("connect.key.done") }}</Button></div>
      </template>
      <ErrorText :error="firstError(createKey)" />
    </div>
    <div class="mb-4 grid gap-3 sm:grid-cols-3">
      <label class="grid gap-1 text-sm">{{ t("connect.fields.address") }}
        <Input v-model="baseUrl" type="url" spellcheck="false" data-testid="connect-address" />
      </label>
      <label class="grid gap-1 text-sm">{{ t("connect.fields.meter") }}
        <NativeSelect v-model="meter" data-testid="connect-meter">
          <option v-for="m in meters.data.value ?? []" :key="m.code" :value="m.code">{{ m.code }}</option>
        </NativeSelect>
      </label>
      <label class="grid gap-1 text-sm">{{ t("connect.fields.customer") }}
        <Input v-model="customer" spellcheck="false" data-testid="connect-customer" />
      </label>
    </div>
    <SegmentedControl v-model="lang" :options="langOptions" :label="t('connect.language')" class="mb-4" data-testid="connect-lang" />

    <template v-for="block in (['install', 'send', 'webhook'] as const)" :key="block">
      <div class="mb-1 mt-4 flex items-center justify-between gap-2">
        <h3 class="text-sm font-semibold">{{ t(`connect.blocks.${block}`) }}</h3>
        <Button type="button" variant="ghost" size="sm" :data-testid="`connect-copy-${block}`" @click="copy(block)"><Copy class="size-4" aria-hidden="true" />{{ copied === block ? t("common.copied") : t("connect.copy") }}</Button>
      </div>
      <pre class="bg-muted mono overflow-x-auto rounded-2xl p-4 text-xs leading-relaxed" :data-testid="`connect-code-${block}`"><code>{{ code[block] }}</code></pre>
      <div v-if="block === 'send' && !live && canWrite" class="bg-muted/40 mt-3 grid gap-3 rounded-2xl p-4" data-testid="connect-test-event">
        <h4 class="text-sm font-semibold">{{ t("connect.testEvent.title") }}</h4>
        <p class="text-muted-foreground text-xs">{{ eventReady ? t("connect.testEvent.help", { meter, customer: customer.trim() }) : t("connect.testEvent.need") }}</p>
        <div><Button type="button" variant="outline" :disabled="testEvent.isPending.value || !eventReady" data-testid="connect-test-event-send" @click="sendTestEvent"><Send class="size-4" aria-hidden="true" />{{ t("connect.testEvent.send") }}</Button></div>
        <p v-if="testEvent.data.value" class="flex flex-wrap items-center gap-2 text-sm" role="status" data-testid="connect-test-event-result" :data-outcome="testEvent.data.value.status">
          <Badge :variant="testEvent.data.value.status === 'rejected' ? 'destructive' : 'success'">{{ t(`connect.testEvent.badge.${testEvent.data.value.status}`) }}</Badge>
          <span>{{ t(`connect.testEvent.${testEvent.data.value.status}`, { code: testEvent.data.value.error_code ?? "", message: testEvent.data.value.error_message ?? "" }) }}</span>
        </p>
        <p v-if="eventErrorHintKey(testEvent.data.value?.error_code)" class="text-muted-foreground text-xs" data-testid="connect-test-event-hint">{{ t(eventErrorHintKey(testEvent.data.value?.error_code) ?? "") }}</p>
        <ErrorText :error="firstError(testEvent)" />
      </div>
      <p v-if="block === 'webhook'" class="text-muted-foreground mt-2 text-xs">{{ t("connect.webhookHelp") }} <Link :to="`${base}/${tenantId}/webhooks`" class="text-primary hover:underline">{{ t("connect.webhookLink") }}</Link></p>
      <div v-if="block === 'webhook' && canWrite" class="bg-muted/40 mt-3 grid gap-3 rounded-2xl p-4" data-testid="connect-webhook-test">
        <h4 class="text-sm font-semibold">{{ t("connect.webhookTest.title") }}</h4>
        <p v-if="hooks.length === 0" class="text-muted-foreground text-xs" data-testid="connect-webhook-none">{{ t("connect.webhookTest.none") }}</p>
        <template v-else>
          <p class="text-muted-foreground text-xs">{{ t("connect.webhookTest.help") }}</p>
          <div class="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
            <label class="grid min-w-0 gap-1 text-sm">{{ t("connect.webhookTest.endpoint") }}
              <NativeSelect v-model="endpointId" data-testid="connect-webhook-endpoint">
                <option v-for="e in hooks" :key="e.id" :value="e.id">{{ e.url }}</option>
              </NativeSelect>
            </label>
            <Button type="button" variant="outline" :disabled="hookActions.test.isPending.value || endpointId === ''" data-testid="connect-webhook-send" @click="sendTestWebhook"><Send class="size-4" aria-hidden="true" />{{ t("connect.webhookTest.send") }}</Button>
          </div>
          <p v-if="outcome" class="flex flex-wrap items-center gap-2 text-sm" role="status" data-testid="connect-webhook-result" :data-outcome="outcome">
            <Badge :variant="outcome === 'delivered' ? 'success' : outcome === 'failed' ? 'destructive' : 'secondary'">{{ t(`connect.webhookTest.badge.${outcome}`) }}</Badge>
            <span>{{ t(`connect.webhookTest.${outcome}`, { answer }) }}</span>
          </p>
        </template>
        <ErrorText :error="webhookError" />
      </div>
    </template>
  </Card>

  <RejectedEvents :tenant-id="tenantId" />
</template>
