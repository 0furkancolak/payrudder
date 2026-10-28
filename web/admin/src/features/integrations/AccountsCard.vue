<script setup lang="ts">
import { Plus } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useLiveProviders } from "@/features/product/api";
import type { LiveProviderName } from "@/features/product/liveProvider";
import { useLiveRouting, useMakeDefaultAccount, useSetLiveRouting, type RoutingRule } from "@/features/providers/connectApi";
import { accountRows, splitAccountKey, type AppRow } from "@/features/providers/connections";
import ProviderSheet from "@/features/providers/ProviderSheet.vue";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { StatusPill } from "@/shared/ui/status-pill";
import { stateTone } from "./status";

/**
 * The payment accounts of an app, one row each: an app can hold several accounts at one provider (a Paddle account per region, an iyzico account per
 * company), tell them apart by a label, pick the default and say which one takes the customers of which country. The first rule that matches a customer's
 * country and the currency wins; everyone else is charged through the default account.
 */
const props = defineProps<{ appId: string; apps: readonly AppRow[]; isOwner: boolean }>();
const { t } = useI18n();

const accounts = useLiveProviders(() => props.appId);
const rows = computed(() => accountRows(accounts.data.value?.accounts).filter((r) => r.provider !== "banktransfer"));
const makeDefault = useMakeDefaultAccount(() => props.appId);
const nameOf = (p: string) => t(`product.goLive.provider.names.${p}`);

// --- the sheet: a further account is added or managed through the provider's own form
const sheetOpen = ref(false);
const sheetProvider = ref<LiveProviderName | "">("");
const sheetLabel = ref("");
const sheetAdding = ref(false);
function add(provider: LiveProviderName) {
  sheetProvider.value = provider;
  sheetLabel.value = "";
  sheetAdding.value = true;
  sheetOpen.value = true;
}
function manage(key: string) {
  const { provider, label } = splitAccountKey(key);
  sheetProvider.value = provider as LiveProviderName;
  sheetLabel.value = label;
  sheetAdding.value = false;
  sheetOpen.value = true;
}

// --- routing rules: edited as a list, saved as a whole
const routing = useLiveRouting(() => props.appId);
const setRouting = useSetLiveRouting(() => props.appId);
const draft = ref<RoutingRule[]>([]);
const saved = ref(false);
watch(
  () => routing.data.value,
  (rules) => {
    draft.value = (rules ?? []).map((r) => ({ ...r }));
  },
  { immediate: true },
);
const dirty = computed(() => JSON.stringify(draft.value) !== JSON.stringify(routing.data.value ?? []));
const accountOptions = computed(() => rows.value.map((r) => ({ key: r.key, label: r.label ? `${nameOf(r.provider)} (${r.label})` : nameOf(r.provider) })));
function addRule() {
  draft.value.push({ country: "", currency: "", provider: rows.value[0]?.key ?? "" });
}
function save() {
  saved.value = false;
  setRouting.mutate(
    draft.value.map((r) => ({ country: r.country.trim().toUpperCase(), currency: r.currency.trim().toUpperCase(), provider: r.provider })),
    { onSuccess: () => (saved.value = true) },
  );
}
</script>

<template>
  <section class="surface flex flex-col gap-5 p-5" data-testid="accounts-card">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold tracking-tight">{{ t("product.providers.account.title") }}</h2>
        <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.providers.account.intro") }}</p>
      </div>
      <div v-if="isOwner" class="flex flex-wrap gap-2">
        <Button v-for="p in (['iyzico', 'paddle', 'stripe'] as const)" :key="p" variant="outline" size="sm" :data-testid="`account-add-${p}`" @click="add(p)">
          <Plus class="size-4" />{{ t("product.providers.account.add", { provider: nameOf(p) }) }}
        </Button>
      </div>
    </div>

    <p v-if="rows.length === 0" class="text-muted-foreground text-sm" data-testid="accounts-empty">{{ t("product.providers.account.empty") }}</p>
    <ul v-else class="divide-border flex flex-col divide-y" :aria-label="t('product.providers.account.title')">
      <li v-for="r in rows" :key="r.key" class="flex flex-wrap items-center justify-between gap-3 py-3" :data-testid="`account-${r.key}`">
        <div class="flex min-w-0 items-center gap-2">
          <span class="font-medium">{{ nameOf(r.provider) }}</span>
          <span v-if="r.label" class="mono bg-muted rounded-md px-1.5 py-0.5 text-xs">{{ r.label }}</span>
          <StatusPill :tone="stateTone(r.state)">{{ t(`product.integrations.state.${r.state}`) }}</StatusPill>
          <StatusPill v-if="r.isDefault" tone="info" :data-testid="`account-default-${r.key}`">{{ t("product.providers.account.default") }}</StatusPill>
        </div>
        <div class="flex items-center gap-2">
          <Button v-if="isOwner && !r.isDefault" variant="ghost" size="sm" :disabled="makeDefault.isPending.value" :data-testid="`account-make-default-${r.key}`" @click="makeDefault.mutate(r.key)">
            {{ t("product.providers.account.makeDefault") }}
          </Button>
          <Button v-if="r.label" variant="outline" size="sm" :disabled="!isOwner" :data-testid="`account-manage-${r.key}`" @click="manage(r.key)">{{ t("product.providers.manage") }}</Button>
        </div>
      </li>
    </ul>
    <ErrorText :error="makeDefault.error.value" />

    <div v-if="rows.length > 1 || draft.length > 0" class="flex flex-col gap-3" data-testid="routing-editor">
      <div>
        <h3 class="text-sm font-semibold">{{ t("product.providers.account.routing.title") }}</h3>
        <p class="text-muted-foreground text-xs">{{ t("product.providers.account.routing.intro") }}</p>
      </div>
      <ul v-if="draft.length > 0" class="flex flex-col gap-2">
        <li v-for="(rule, i) in draft" :key="i" class="grid items-end gap-2 sm:grid-cols-[6rem_6rem_1fr_auto]" :data-testid="`routing-rule-${i}`">
          <label class="grid gap-1 text-xs">{{ t("product.providers.account.routing.country") }}
            <Input v-model="rule.country" class="mono uppercase" maxlength="2" :placeholder="t('product.providers.account.routing.any')" :disabled="!isOwner" :data-testid="`routing-country-${i}`" />
          </label>
          <label class="grid gap-1 text-xs">{{ t("product.providers.account.routing.currency") }}
            <Input v-model="rule.currency" class="mono uppercase" maxlength="3" :placeholder="t('product.providers.account.routing.any')" :disabled="!isOwner" :data-testid="`routing-currency-${i}`" />
          </label>
          <label class="grid gap-1 text-xs">{{ t("product.providers.account.routing.account") }}
            <NativeSelect v-model="rule.provider" :disabled="!isOwner" :data-testid="`routing-account-${i}`">
              <option v-for="o in accountOptions" :key="o.key" :value="o.key">{{ o.label }}</option>
            </NativeSelect>
          </label>
          <Button v-if="isOwner" variant="ghost" size="sm" :data-testid="`routing-remove-${i}`" @click="draft.splice(i, 1)">{{ t("product.providers.account.routing.remove") }}</Button>
        </li>
      </ul>
      <div v-if="isOwner" class="flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" type="button" data-testid="routing-add" @click="addRule">{{ t("product.providers.account.routing.addRule") }}</Button>
        <Button size="sm" type="button" :disabled="!dirty || setRouting.isPending.value" data-testid="routing-save" @click="save">{{ t("product.providers.account.routing.save") }}</Button>
        <span v-if="saved && !dirty" class="text-success text-xs" role="status" data-testid="routing-saved">{{ t("product.providers.account.routing.saved") }}</span>
      </div>
      <ErrorText :error="setRouting.error.value" />
    </div>

    <ProviderSheet v-model:open="sheetOpen" :provider="sheetProvider" :tag="sheetLabel" :adding="sheetAdding" :app-id="appId" :apps="apps" scope="app" :can-org="false" :is-owner="isOwner" />
  </section>
</template>
