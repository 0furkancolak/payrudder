<script setup lang="ts">
import { CreditCard } from "@lucide/vue";
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import BankTransferForm from "@/features/providers/BankTransferForm.vue";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useApp, useLiveProviderActions, useLiveProviders, useMe } from "./api";
import { fromTenant } from "./billingProfile";
import { LIVE_PROVIDERS, PROVIDER_FIELDS, buildProviderSettings, isLiveProvider, prefilled, problemCode, type LiveProviderName, type ProviderErrors } from "./liveProvider";

/**
 * Connecting a payment provider to the live environment: the "payment provider" item of the go-live checklist. Only the owner connects or removes one. The keys
 * are typed here once; the server asks the provider to accept them before it stores them (sealed), and they are never shown again: after a save the form is empty.
 * The list below shows each live account, whether the provider accepts it now, and why not when it does not.
 */
const props = defineProps<{ appId: string }>();
const { t } = useI18n();
const app = useApp(() => props.appId);
const live = computed(() => app.data.value?.app?.tenants?.find((x) => x.environment === "ENVIRONMENT_LIVE"));
const isOwner = computed(() => app.data.value?.role === "owner");
const me = useMe();
const providers = useLiveProviders(() => props.appId, () => !!live.value);
const { connect, remove } = useLiveProviderActions(() => props.appId);

const accounts = computed(() => providers.data.value?.accounts ?? []);
const provider = ref<LiveProviderName | "">("");
const values = reactive<Record<string, string>>({});
const errors = ref<ProviderErrors>({});
const bankForm = ref<InstanceType<typeof BankTransferForm> | null>(null);
const connectedName = ref("");

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

// Choosing a provider starts an empty form, with the buyer details filled from the billing profile where they are already known.
watch(provider, (p) => {
  for (const k of Object.keys(values)) delete values[k];
  errors.value = {};
  if (p === "") return;
  connectedName.value = "";
  connect.reset();
  if (p === "iyzico" || p === "paddle") Object.assign(values, prefilled(p, known.value));
});

const typed = computed(() => (provider.value === "iyzico" || provider.value === "paddle" ? PROVIDER_FIELDS[provider.value] : []));
const fieldKey = (path: string) => path.replace(/\./g, "_");
const label = (path: string) => t(`product.goLive.provider.fields.${fieldKey(path)}`);
const error = (path: string) => (errors.value[path] ? t(`product.goLive.provider.problems.${errors.value[path]}`) : "");
const providerName = (p: string) => (isLiveProvider(p) ? t(`product.goLive.provider.names.${p}`) : p);

function submit() {
  const p = provider.value;
  if (p === "" || !isOwner.value) return;
  let settings: Record<string, unknown> | null = null;
  if (p === "banktransfer") {
    settings = (bankForm.value?.build() as Record<string, unknown> | null) ?? null;
  } else {
    const built = buildProviderSettings(p, values);
    errors.value = built.ok ? {} : built.errors;
    settings = built.ok ? built.settings : null;
  }
  if (!settings) return;
  connect.mutate(
    { provider: p, settings },
    {
      onSuccess: () => {
        // The secrets leave the form as soon as they are stored.
        connectedName.value = p;
        for (const k of Object.keys(values)) values[k] = "";
        bankForm.value?.reset();
        provider.value = "";
      },
    },
  );
}

function removeAccount(p: string) {
  if (isLiveProvider(p) && window.confirm(t("product.goLive.provider.confirmRemove", { provider: providerName(p) }))) remove.mutate(p);
}
</script>

<template>
  <section id="live-provider" class="surface flex flex-col gap-4 p-6" :aria-label="t('product.goLive.provider.title')" data-testid="live-provider">
    <div class="flex items-start gap-3">
      <span class="bg-brand-soft text-brand-strong flex size-10 shrink-0 items-center justify-center rounded-2xl" aria-hidden="true"><CreditCard class="size-5" /></span>
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.goLive.provider.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("product.goLive.provider.intro") }}</p>
      </div>
    </div>

    <Skeleton v-if="app.isPending.value || providers.isPending.value" class="h-24 rounded-2xl" />
    <p v-else-if="!live" class="text-muted-foreground text-sm">{{ t("product.goLive.noLive") }}</p>
    <template v-else>
      <ErrorText :error="providers.error.value" />
      <ul v-if="accounts.length > 0" class="flex flex-col gap-2" :aria-label="t('product.goLive.provider.connected')" data-testid="live-accounts">
        <li v-for="a in accounts" :key="a.provider" class="bg-muted flex flex-wrap items-center justify-between gap-3 rounded-2xl px-4 py-3" :data-testid="`live-account-${a.provider}`" :data-working="a.working">
          <div class="min-w-0">
            <p class="text-sm font-medium">
              {{ providerName(a.provider ?? "") }}
              <span v-if="a.default" class="text-muted-foreground text-xs font-normal">- {{ t("product.goLive.provider.default") }}</span>
            </p>
            <p v-if="problemCode(a.problem)" class="text-destructive text-xs" data-testid="live-account-problem">{{ t(`product.goLive.provider.accountProblems.${problemCode(a.problem)}`) }}</p>
            <p v-else class="text-muted-foreground text-xs">{{ a.checked ? t("product.goLive.provider.checkedYes") : t("product.goLive.provider.checkedNo") }}</p>
          </div>
          <div class="flex items-center gap-3">
            <StatusPill :tone="a.working ? 'success' : 'danger'">{{ a.working ? t("product.goLive.provider.working") : t("product.goLive.provider.notWorking") }}</StatusPill>
            <Button v-if="isOwner" variant="ghost" size="sm" :disabled="remove.isPending.value" :data-testid="`live-account-remove-${a.provider}`" @click="removeAccount(a.provider ?? '')">{{ t("product.goLive.provider.remove") }}</Button>
          </div>
        </li>
      </ul>
      <p v-else class="text-muted-foreground text-sm" data-testid="live-accounts-none">{{ t("product.goLive.provider.none") }}</p>
      <p v-if="connectedName" class="text-success text-sm" role="status" data-testid="provider-connected">{{ t("product.goLive.provider.connectedNow", { provider: providerName(connectedName) }) }}</p>
      <ErrorText :error="remove.error.value" />

      <p v-if="!isOwner" class="text-muted-foreground text-xs" data-testid="provider-owner-only">{{ t("product.goLive.provider.ownerOnly") }}</p>
      <form v-else class="grid gap-3 border-t pt-4" :aria-label="t('product.goLive.provider.connectTitle')" novalidate autocomplete="off" @submit.prevent="submit">
        <h3 class="text-base font-semibold">{{ t("product.goLive.provider.connectTitle") }}</h3>
        <div class="grid gap-1">
          <label class="text-sm" for="lp-provider">{{ t("product.goLive.provider.provider") }}</label>
          <NativeSelect id="lp-provider" v-model="provider" data-testid="provider-select">
            <option value="">{{ t("product.goLive.provider.choose") }}</option>
            <option v-for="p in LIVE_PROVIDERS" :key="p" :value="p">{{ providerName(p) }}</option>
          </NativeSelect>
        </div>
        <template v-if="provider === 'banktransfer'">
          <BankTransferForm ref="bankForm" environment="live" />
        </template>
        <template v-else-if="provider !== ''">
          <p class="text-muted-foreground text-xs">{{ t("product.goLive.provider.secretsOnce") }}</p>
          <div class="grid gap-3 sm:grid-cols-2">
            <div v-for="f in typed" :key="f.path" class="grid gap-1">
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
            </div>
          </div>
        </template>
        <div v-if="provider !== ''" class="flex flex-wrap items-center gap-3">
          <Button type="submit" :disabled="connect.isPending.value" data-testid="provider-connect">{{ connect.isPending.value ? t("product.goLive.provider.checking") : provider === "banktransfer" ? t("product.goLive.provider.saveBank") : t("product.goLive.provider.checkAndConnect") }}</Button>
        </div>
        <ErrorText v-if="provider !== ''" :error="connect.error.value" />
      </form>
    </template>
  </section>
</template>
