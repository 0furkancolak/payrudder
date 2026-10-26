<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { fromTenant } from "@/features/product/billingProfile";
import { useApp, useLiveProviders, useMe } from "@/features/product/api";
import { PROVIDER_FIELDS, buildProviderSettings, prefilled, problemCode, type LiveProviderName, type ProviderErrors } from "@/features/product/liveProvider";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { SegmentedControl } from "@/shared/ui/segmented";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { StatusPill } from "@/shared/ui/status-pill";
import BankTransferForm from "./BankTransferForm.vue";
import { useAppsProviders, useConnectProvider, useDisconnectProvider } from "./connectApi";
import { PROVIDER_LINKS, connectionOf, helpKey, summarize, targetsOf, type AppResult, type AppRow, type Scope } from "./connections";

/**
 * The side sheet that connects one payment provider (or replaces its keys, tests it, or disconnects it). The keys are typed once, are password fields, are
 * checked by the provider before they are stored and are never shown again: after a save the form is empty. "This app only" acts on one app; "All apps of the
 * organisation" repeats the same connection on every app that has a live environment, through each app's own endpoint. The sandbox needs no connection:
 * it runs against the built-in simulator.
 */
const props = defineProps<{
  provider: LiveProviderName | "";
  /** The app the sheet was opened for ("" on the organisation screen without an app picked). */
  appId: string;
  apps: readonly AppRow[];
  scope: Scope;
  /** The organisation-wide choice is offered only to an owner of an organisation with more than one app that can take it. */
  canOrg: boolean;
  isOwner: boolean;
}>();
const open = defineModel<boolean>("open", { required: true });
const emit = defineEmits<{ done: [summary: { provider: LiveProviderName; scope: Scope; ok: number; failed: number }] }>();

const { t } = useI18n();
const errorText = useErrorText();
const scope = ref<Scope>(props.scope);
const values = reactive<Record<string, string>>({});
const errors = ref<ProviderErrors>({});
const bankForm = ref<InstanceType<typeof BankTransferForm> | null>(null);
const failures = ref<AppResult[]>([]);
const tested = ref<"working" | "problem" | null>(null);
const testProblem = ref<string | null>(null);

const connect = useConnectProvider();
const disconnect = useDisconnectProvider();

const names = computed(() => props.apps.filter((a) => a.hasLive));
const targets = computed(() => targetsOf(scope.value, props.appId, props.apps));
const prefillApp = computed(() => (props.appId !== "" ? props.appId : (names.value[0]?.id ?? "")));
const app = useApp(prefillApp);
const me = useMe();
const live = computed(() => app.data.value?.app?.tenants?.find((x) => x.environment === "ENVIRONMENT_LIVE"));
const own = useLiveProviders(() => props.appId, () => props.appId !== "" && !!props.provider);
const all = useAppsProviders(() => props.apps);

/** The accounts of the apps this sheet would act on, for "connected here" and the disconnect button. */
const connectedTargets = computed(() => targets.value.filter((a) => connectionOf(all.value.find((r) => r.app.id === a.id)?.accounts, props.provider).state !== "none"));
const here = computed(() => connectionOf(own.data.value?.accounts, props.provider));
const providerName = computed(() => (props.provider ? t(`product.goLive.provider.names.${props.provider}`) : ""));
const typed = computed(() => (props.provider === "iyzico" || props.provider === "paddle" ? PROVIDER_FIELDS[props.provider] : []));
const fieldKey = (path: string) => path.replace(/\./g, "_");
const label = (path: string) => t(`product.goLive.provider.fields.${fieldKey(path)}`);
const error = (path: string) => (errors.value[path] ? t(`product.goLive.provider.problems.${errors.value[path]}`) : "");
const help = (path: string) => {
  const k = props.provider === "iyzico" || props.provider === "paddle" ? helpKey(props.provider, path) : null;
  return k ? t(k) : "";
};
const link = computed(() => (props.provider ? PROVIDER_LINKS[props.provider] : null));
const scopeOptions = computed(() => [
  { value: "app", label: t("product.providers.scope.app") },
  { value: "org", label: t("product.providers.scope.org") },
]);

const known = computed(() => {
  const f = fromTenant(live.value);
  let country = f.country;
  try {
    country = f.country ? (new Intl.DisplayNames(["en"], { type: "region" }).of(f.country) ?? f.country) : "";
  } catch {
    /* keep the code */
  }
  return { email: me.data.value?.user?.email ?? "", taxId: f.taxId, line1: f.line1, city: f.city, country, postalCode: f.postalCode, legalName: f.legalName };
});

function clear() {
  for (const k of Object.keys(values)) delete values[k];
  errors.value = {};
  failures.value = [];
  tested.value = null;
  testProblem.value = null;
  connect.reset();
  disconnect.reset();
  bankForm.value?.reset();
}
// A sheet that opens starts clean, in the scope it was opened for, with the buyer details filled in from the billing profile where they are already known.
watch(
  () => [open.value, props.provider, props.scope] as const,
  ([isOpen, p, s]) => {
    if (!isOpen) {
      clear();
      return;
    }
    scope.value = s;
    clear();
    if (p === "iyzico" || p === "paddle") Object.assign(values, prefilled(p, known.value));
  },
  { immediate: true },
);
// The profile arrives after the sheet opened: fill what was left empty, never what the person typed.
watch(known, (k) => {
  const p = props.provider;
  if (!open.value || (p !== "iyzico" && p !== "paddle")) return;
  for (const [path, v] of Object.entries(prefilled(p, k))) if (!values[path] && v) values[path] = v;
});

function submit() {
  const p = props.provider;
  if (p === "" || !props.isOwner || targets.value.length === 0) return;
  let settings: Record<string, unknown> | null;
  if (p === "banktransfer") {
    settings = (bankForm.value?.build() as Record<string, unknown> | null) ?? null;
  } else {
    const built = buildProviderSettings(p, values);
    errors.value = built.ok ? {} : built.errors;
    settings = built.ok ? built.settings : null;
  }
  if (!settings) return;
  const used = scope.value;
  failures.value = [];
  connect.mutate(
    { apps: targets.value, provider: p, settings },
    {
      onSuccess: (results) => {
        const s = summarize(results);
        failures.value = results.filter((r) => !r.ok);
        // The secrets leave the form once they are stored everywhere; when an app refused them they stay for another try (they are only what was just typed).
        if (s.failed === 0) {
          for (const k of Object.keys(values)) values[k] = "";
          bankForm.value?.reset();
        }
        emit("done", { provider: p, scope: used, ok: s.ok, failed: s.failed });
        if (s.failed === 0) open.value = false;
      },
    },
  );
}

async function test() {
  tested.value = null;
  const r = await own.refetch();
  const c = connectionOf(r.data?.accounts, props.provider);
  tested.value = c.state === "working" ? "working" : "problem";
  testProblem.value = c.problem;
}

function removeNow() {
  const p = props.provider;
  if (p === "" || connectedTargets.value.length === 0) return;
  if (!window.confirm(t(scope.value === "org" ? "product.providers.confirmDisconnectOrg" : "product.goLive.provider.confirmRemove", { provider: providerName.value, count: connectedTargets.value.length }))) return;
  disconnect.mutate(
    { apps: connectedTargets.value, provider: p },
    {
      onSuccess: (results) => {
        failures.value = results.filter((r) => !r.ok);
        emit("done", { provider: p, scope: scope.value, ok: summarize(results).ok, failed: summarize(results).failed });
        if (failures.value.length === 0) open.value = false;
      },
    },
  );
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="sm:max-w-xl" data-testid="provider-sheet">
      <SheetHeader>
        <SheetTitle>{{ here.state !== "none" && scope === "app" ? t("product.providers.sheet.manageTitle", { provider: providerName }) : t("product.providers.sheet.connectTitle", { provider: providerName }) }}</SheetTitle>
        <SheetDescription>{{ t(`product.providers.sheet.intro.${provider || 'iyzico'}`) }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <p v-if="!isOwner" class="text-muted-foreground text-sm" data-testid="provider-owner-only">{{ t("product.goLive.provider.ownerOnly") }}</p>

        <div v-if="canOrg" class="grid gap-1.5">
          <span class="text-sm font-medium">{{ t("product.providers.scope.title") }}</span>
          <SegmentedControl v-model="scope" :options="scopeOptions" :label="t('product.providers.scope.title')" data-testid="provider-scope" />
          <p class="text-muted-foreground text-xs" data-testid="provider-scope-note">
            {{ scope === "org" ? t("product.providers.scope.orgNote", { count: targets.length }) : t("product.providers.scope.appNote") }}
          </p>
        </div>

        <p class="bg-muted text-muted-foreground rounded-xl px-3 py-2 text-xs">{{ t("product.providers.sheet.envNote") }}</p>

        <div v-if="scope === 'app' && here.state !== 'none'" class="bg-muted flex flex-wrap items-center justify-between gap-3 rounded-2xl px-4 py-3" data-testid="provider-sheet-status">
          <div class="min-w-0">
            <StatusPill :tone="here.state === 'working' ? 'success' : 'danger'">{{ here.state === "working" ? t("product.goLive.provider.working") : t("product.goLive.provider.notWorking") }}</StatusPill>
            <p v-if="here.problem" class="text-destructive mt-1 text-xs">{{ t(`product.goLive.provider.accountProblems.${here.problem}`) }}</p>
            <p v-else class="text-muted-foreground mt-1 text-xs">{{ here.checked ? t("product.goLive.provider.checkedYes") : t("product.goLive.provider.checkedNo") }}</p>
            <p v-if="tested" class="mt-1 text-xs" role="status" data-testid="provider-test-result" :class="tested === 'working' ? 'text-success' : 'text-destructive'">
              {{ tested === "working" ? t("product.providers.sheet.testOk") : t("product.providers.sheet.testFailed", { reason: t(`product.goLive.provider.accountProblems.${problemCode(testProblem ?? 'refused')}`) }) }}
            </p>
          </div>
          <Button variant="outline" size="sm" :disabled="own.isFetching.value" data-testid="provider-test" @click="test">{{ own.isFetching.value ? t("product.providers.sheet.testing") : t("product.providers.sheet.test") }}</Button>
        </div>

        <form id="provider-sheet-form" class="grid gap-3" novalidate autocomplete="off" :aria-label="t('product.goLive.provider.connectTitle')" @submit.prevent="submit">
          <template v-if="provider === 'banktransfer'">
            <BankTransferForm ref="bankForm" environment="live" />
          </template>
          <template v-else-if="provider !== ''">
            <p class="text-muted-foreground text-xs">
              {{ here.state !== "none" && scope === "app" ? t("product.providers.sheet.replaceNote") : t("product.goLive.provider.secretsOnce") }}
              <a v-if="link" :href="link" target="_blank" rel="noopener noreferrer" class="text-primary font-medium hover:underline">{{ t("product.providers.sheet.openDashboard", { provider: providerName }) }}</a>
            </p>
            <div class="grid gap-3 sm:grid-cols-2">
              <div v-for="f in typed" :key="f.path" class="grid gap-1" :class="f.secret || f.path === 'usage_product_id' ? 'sm:col-span-2' : ''">
                <label class="text-sm" :for="`lp-${fieldKey(f.path)}`">{{ label(f.path) }}<span v-if="f.optional" class="text-muted-foreground"> ({{ t("product.goLive.provider.optional") }})</span></label>
                <Input
                  :id="`lp-${fieldKey(f.path)}`"
                  v-model="values[f.path]"
                  :type="f.secret ? 'password' : 'text'"
                  :class="f.secret ? 'mono' : ''"
                  spellcheck="false"
                  :autocomplete="f.secret ? 'new-password' : 'off'"
                  :aria-invalid="!!errors[f.path]"
                  :data-testid="`lp-${fieldKey(f.path)}`"
                />
                <p v-if="error(f.path)" class="text-destructive text-xs" role="alert">{{ error(f.path) }}</p>
                <p v-else-if="help(f.path)" class="text-muted-foreground text-xs">{{ help(f.path) }}</p>
              </div>
            </div>
          </template>
        </form>

        <ErrorText v-if="connect.error.value || disconnect.error.value" :error="connect.error.value ?? disconnect.error.value" />
        <ul v-if="failures.length > 0" class="flex flex-col gap-1" data-testid="provider-failures">
          <li v-for="f in failures" :key="f.appId" class="text-destructive text-sm" role="alert">{{ f.name }}: {{ errorText(f.error) }}</li>
        </ul>
        <p v-if="failures.length > 0 && connect.data.value && connect.data.value.some((r) => r.ok)" class="text-muted-foreground text-xs">{{ t("product.providers.sheet.partial", { ok: connect.data.value.filter((r) => r.ok).length }) }}</p>
      </SheetBody>
      <SheetFooter>
        <Button v-if="isOwner && connectedTargets.length > 0" variant="ghost" class="text-destructive me-auto" type="button" :disabled="disconnect.isPending.value" data-testid="provider-disconnect" @click="removeNow">
          {{ scope === "org" ? t("product.providers.sheet.disconnectOrg", { count: connectedTargets.length }) : t("product.goLive.provider.remove") }}
        </Button>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="provider-sheet-form" :disabled="!isOwner || connect.isPending.value || targets.length === 0" data-testid="provider-connect">
          {{ connect.isPending.value ? t("product.goLive.provider.checking") : provider === "banktransfer" ? t("product.goLive.provider.saveBank") : t("product.goLive.provider.checkAndConnect") }}
        </Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
