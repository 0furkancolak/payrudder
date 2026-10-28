<script setup lang="ts">
import { Check, CircleDashed, Copy } from "@lucide/vue";
import { Link } from "@tanstack/vue-router";
import { useQuery } from "@tanstack/vue-query";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useApps } from "@/features/product/api";
import { useMeters } from "@/features/meters/api";
import { useCustomers } from "@/features/customers/api";
import { seriesOptions } from "@/features/usage/api";
import { useTenants } from "@/features/tenants/api";
import { copyText } from "@/shared/lib/clipboard";
import { findTenant } from "@/shared/lib/environment";
import { useRouteTenantId } from "@/shared/lib/routeTenant";
import { isOperator, tenantBase } from "@/shared/lib/session";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { PageHeader } from "@/shared/ui/page-header";
import { SegmentedControl } from "@/shared/ui/segmented";
import { LANGS, keyHintOf, snippets, type Lang } from "./snippets";

/**
 * Connect an app: the steps from "I have an app" to "my first event arrived", with the code to paste. The code is built from the app's own meter and a customer id,
 * for the environment the page is in (a test key shape for the sandbox, a live one for live). The checklist turns green as the things happen: a meter exists, a
 * customer exists, and a usage event has been counted (asked for every few seconds while the page is open). The API address is the deployment's, which the console
 * cannot know, so it is typed once and kept in this browser.
 */
const { t } = useI18n();
const tenantId = useRouteTenantId();
const apps = useApps();
const operatorTenants = useTenants({ enabled: isOperator });
const live = computed(() => {
  const id = tenantId.value;
  const env = findTenant(apps.data.value?.apps, id)?.tenant.environment ?? operatorTenants.data.value?.tenants?.find((x) => x.id === id)?.environment;
  return env === "ENVIRONMENT_LIVE";
});

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
const baseUrl = ref(readAddress() || "http://localhost:42800");
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

const steps = computed(() => [
  { key: "key", done: false, optional: true },
  { key: "meter", done: (meters.data.value ?? []).length > 0 },
  { key: "customer", done: (customers.data.value?.customers ?? []).length > 0 },
  { key: "event", done: arrived.data.value === true },
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
        </span>
      </li>
    </ol>
    <p v-if="!arrived.data.value" class="text-muted-foreground mt-3 text-xs" role="status" data-testid="connect-waiting">{{ t("connect.waiting") }}</p>
  </Card>

  <Card class="mb-4 p-5">
    <h2 class="mb-1 text-base font-semibold">{{ t("connect.codeTitle") }}</h2>
    <p class="text-muted-foreground mb-3 text-sm">{{ t(live ? "connect.keyLive" : "connect.keyTest") }}</p>
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
      <p v-if="block === 'webhook'" class="text-muted-foreground mt-2 text-xs">{{ t("connect.webhookHelp") }} <Link :to="`${base}/${tenantId}/webhooks`" class="text-primary hover:underline">{{ t("connect.webhookLink") }}</Link></p>
    </template>
  </Card>
</template>
