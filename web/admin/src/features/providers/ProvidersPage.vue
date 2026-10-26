<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { Eye, Plus, Trash2, X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { useTableState } from "@/shared/lib/useTableState";
import { PROVIDERS, ROUTABLE_PROVIDERS } from "@/shared/lib/currencies";
import { CountrySelect } from "@/shared/ui/location-fields";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Textarea } from "@/shared/ui/textarea";
import { useAccountActions, usePreviewRouting, useProviderAccounts, useProviderSubscriptions, useRoutingRules, useSetRoutingRules, type Rule } from "./api";
import CatalogueCard from "./CatalogueCard.vue";
import BankTransferForm from "./BankTransferForm.vue";
import { templateText } from "./templates";
import { CurrencySelect } from "@/shared/ui/search-select";
import type { Environment } from "@/shared/lib/environment";

const { t } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));

// --- accounts
const accounts = useProviderAccounts(tenantId);
const actions = useAccountActions(tenantId);
const provider = ref("");
const environment = ref<Environment>("live");
const makeDefault = ref(true);
const settings = ref("");
const settingsError = ref("");
const open = ref(false);
// A sheet that was closed opens clean: the last provider, settings and error are not shown again.
watch(open, (v) => {
  if (!v) return;
  provider.value = "";
  environment.value = "live";
  makeDefault.value = true;
  settingsError.value = "";
  actions.save.reset();
});
type Account = NonNullable<NonNullable<typeof accounts.data.value>["accounts"]>[number];
const accountTable = useTableState<Account>(() => accounts.data.value?.accounts ?? [], {
  searchFields: (a) => [a.provider, a.environment],
  filters: { environment: (a, v) => a.environment === v },
});
const all = computed(() => ({ value: "", label: t("common.all") }));
const environments = computed(() => [all.value, { value: "live", label: "live" }, { value: "sandbox", label: "sandbox" }]);
const accountActions = (a: Account) => [{ key: "remove", label: t("billing.common.remove"), icon: Trash2, destructive: true, onSelect: () => removeAccount(a.provider ?? "", (a.environment ?? "live") as Environment) }];
const accountEnv = computed({ get: () => accountTable.filters.value.environment ?? "", set: (v) => (accountTable.filters.value.environment = String(v)) });
/** Bank transfer has a structured form instead of the JSON text. */
const isBank = computed(() => provider.value === "banktransfer");
const bankForm = ref<InstanceType<typeof BankTransferForm> | null>(null);
watch(provider, (p) => (settings.value = p && p !== "banktransfer" ? templateText(p) : ""));
function saveAccount() {
  settingsError.value = "";
  if (isBank.value) {
    const bank = bankForm.value?.build();
    if (!bank) return;
    actions.save.mutate(
      { provider: provider.value, environment: environment.value, makeDefault: makeDefault.value, settings: { ...bank } },
      {
        onSuccess: () => {
          bankForm.value?.reset();
          open.value = false;
        },
      },
    );
    return;
  }
  let parsed: Record<string, unknown>;
  try {
    parsed = JSON.parse(settings.value) as Record<string, unknown>;
  } catch {
    settingsError.value = t("billing.providers.badJson");
    return;
  }
  actions.save.mutate(
    { provider: provider.value, environment: environment.value, makeDefault: makeDefault.value, settings: parsed },
    {
      onSuccess: () => {
        settings.value = templateText(provider.value);
        open.value = false;
      },
    },
  );
}
function removeAccount(p: string, env: Environment) {
  if (window.confirm(t("billing.providers.confirmRemove", { provider: p, environment: env }))) actions.remove.mutate({ provider: p, environment: env });
}
const webhookPath = computed(() => `/webhooks/${provider.value || "{provider}"}/${tenantId.value}/${environment.value}`);

// --- routing
const stored = useRoutingRules(tenantId);
const setRules = useSetRoutingRules(tenantId);
const preview = usePreviewRouting(tenantId);
const rules = ref<Rule[]>([]);
watch(
  () => stored.data.value,
  (v) => (rules.value = (v ?? []).map((r) => ({ ...r }))),
  { immediate: true },
);
const pCountry = ref("");
const pCurrency = ref("");

// --- what providers run
const providerSubs = useProviderSubscriptions(tenantId);
type ProviderSub = NonNullable<NonNullable<typeof providerSubs.data.value>["subscriptions"]>[number];
const subTable = useTableState<ProviderSub>(() => providerSubs.data.value?.subscriptions ?? [], {
  searchFields: (s) => [s.provider, s.provider_ref, s.environment],
  filters: { status: (s, v) => s.status === v },
});
const subStatus = computed({ get: () => subTable.filters.value.status ?? "", set: (v) => (subTable.filters.value.status = String(v)) });
const subStatuses = computed(() => [all.value, ...new Set((providerSubs.data.value?.subscriptions ?? []).map((s) => s.status ?? "").filter(Boolean))].map((s) => (typeof s === "string" ? { value: s, label: s } : s)));
const subDetail = ref<number | null>(null);
const subCurrent = computed(() => (subDetail.value === null ? undefined : subTable.items.value[subDetail.value]));
const subRows = computed(() => {
  const s = subCurrent.value;
  if (!s) return [];
  return [
    { key: "provider", label: t("billing.providers.provider"), value: s.provider },
    { key: "environment", label: t("billing.providers.environment"), value: s.environment },
    { key: "reference", label: t("billing.providers.reference"), value: s.provider_ref, mono: true },
    { key: "status", label: t("billing.subscriptions.state") },
    { key: "next", label: t("billing.providers.nextBilled"), value: when(s.next_billed_at) },
    { key: "updated", label: t("billing.providers.updated"), value: when(s.updated_at) },
  ];
});
const subActions = (i: number) => [{ key: "view", label: t("common.view"), icon: Eye, onSelect: () => (subDetail.value = i) }];
const when = (v?: string) => fmt.date(v, "day", "—");
</script>

<template>
  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 pb-3 text-base font-semibold">{{ t("billing.providers.accounts") }}</h2>
    <DataToolbar v-model:search="accountTable.search.value" :active="accountTable.active.value" @clear="accountTable.clear()">
      <FilterSelect v-model="accountEnv" :label="t('billing.providers.environment')" :options="environments" />
      <template #actions>
        <Button type="button" @click="open = true"><Plus class="size-4" aria-hidden="true" />{{ t("billing.providers.addAccount") }}</Button>
      </template>
    </DataToolbar>
    <ErrorText :error="accounts.error.value" class="px-3" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.providers.provider") }}</TableHead>
          <TableHead>{{ t("billing.providers.environment") }}</TableHead>
          <TableHead>{{ t("billing.providers.default") }}</TableHead>
          <TableHead>{{ t("billing.providers.updated") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="a in accountTable.items.value" :key="`${a.provider}-${a.environment}`">
          <TableCell class="font-medium">{{ a.provider }}</TableCell>
          <TableCell>{{ a.environment }}</TableCell>
          <TableCell><StatusPill v-if="a.is_default" tone="success">{{ t("billing.providers.default") }}</StatusPill></TableCell>
          <TableCell>{{ when(a.updated_at) }}</TableCell>
          <TableCell><RowActions :actions="accountActions(a)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!accounts.isPending.value && (accounts.data.value?.accounts ?? []).length === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.providers.noAccounts") }}</p>
    <p v-else-if="accountTable.total.value === 0 && accountTable.active.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-model:page="accountTable.page.value" v-model:size="accountTable.size.value" :pages="accountTable.pages.value" :total="accountTable.total.value" />
    <ErrorText :error="actions.remove.error.value" class="px-3" />
    <p v-if="actions.save.isSuccess.value" class="text-success px-3 text-sm" role="status">{{ t("billing.providers.saved") }}</p>

    <Sheet v-model:open="open">
      <SheetContent class="sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>{{ t("billing.providers.addAccount") }}</SheetTitle>
          <SheetDescription>{{ t("billing.providers.accountsHint") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form id="provider-account-form" class="grid gap-3" role="form" :aria-label="t('billing.providers.addAccount')" @submit.prevent="saveAccount">
            <div class="grid gap-3 sm:grid-cols-2">
              <NativeSelect v-model="provider" :aria-label="t('billing.providers.provider')" required>
                <option value="" disabled>{{ t("billing.providers.provider") }}</option>
                <option v-for="p in PROVIDERS" :key="p.name" :value="p.name">{{ p.name }}</option>
              </NativeSelect>
              <NativeSelect v-model="environment" :aria-label="t('billing.providers.environment')">
                <option value="live">live</option>
                <option value="sandbox">sandbox</option>
              </NativeSelect>
            </div>
            <label class="flex items-center gap-2 text-sm"><Checkbox v-model="makeDefault" :aria-label="t('billing.providers.makeDefault')" />{{ t("billing.providers.makeDefault") }}</label>
            <BankTransferForm v-if="isBank" ref="bankForm" :environment="environment" />
            <Textarea v-else v-model="settings" rows="10" class="mono text-xs" spellcheck="false" :aria-label="t('billing.providers.settings')" :placeholder="t('billing.providers.settings')" required />
            <p v-if="!isBank" class="text-muted-foreground text-xs">{{ t("billing.providers.webhookHint", { path: webhookPath }) }}</p>
            <ErrorText :message="settingsError" />
            <ErrorText :error="actions.save.error.value" />
          </form>
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="provider-account-form" :disabled="actions.save.isPending.value || provider === ''">{{ t("billing.providers.saveAccount") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  </Card>

  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 pb-3 text-base font-semibold">{{ t("billing.providers.runByProviders") }}</h2>
    <DataToolbar v-model:search="subTable.search.value" :active="subTable.active.value" @clear="subTable.clear()">
      <FilterSelect v-model="subStatus" :label="t('billing.subscriptions.state')" :options="subStatuses" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.providers.reference") }}</TableHead>
          <TableHead>{{ t("billing.providers.provider") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
          <TableHead>{{ t("billing.providers.nextBilled") }}</TableHead>
          <TableHead>{{ t("billing.providers.updated") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(s, i) in subTable.items.value" :key="`${s.provider}-${s.provider_ref}`" class="cursor-pointer" @click="subDetail = i">
          <TableCell class="mono text-xs font-medium">{{ s.provider_ref }}</TableCell>
          <TableCell>{{ s.provider }} <span class="text-muted-foreground text-xs">{{ s.environment }}</span></TableCell>
          <TableCell><StatusPill :tone="toneOf(s.status)">{{ s.status }}</StatusPill></TableCell>
          <TableCell>{{ when(s.next_billed_at) }}</TableCell>
          <TableCell>{{ when(s.updated_at) }}</TableCell>
          <TableCell><RowActions :actions="subActions(i)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!providerSubs.isPending.value && (providerSubs.data.value?.subscriptions ?? []).length === 0" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.providers.noProviderSubscriptions") }}
    </p>
    <p v-else-if="subTable.total.value === 0 && subTable.active.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-model:page="subTable.page.value" v-model:size="subTable.size.value" :pages="subTable.pages.value" :total="subTable.total.value" />
  </Card>

  <DetailSheet v-model:index="subDetail" :total="subTable.items.value.length" :title="subCurrent?.provider_ref ?? ''" :description="subCurrent?.provider ?? ''">
    <DetailList :rows="subRows">
      <template #status><StatusPill :tone="toneOf(subCurrent?.status)">{{ subCurrent?.status }}</StatusPill></template>
    </DetailList>
  </DetailSheet>

  <Card class="mb-6">
    <h2 class="text-base font-semibold">{{ t("billing.providers.routing") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.providers.routingHint") }}</p>
    <ErrorText :error="stored.error.value" />
    <div v-for="(r, i) in rules" :key="i" class="grid grid-cols-[1fr_1fr_1fr_auto] items-center gap-3" role="group" :aria-label="t('billing.providers.rule', { n: i + 1 })">
      <CountrySelect v-model="r.country" code-only :aria-label="t('billing.providers.country', { n: i + 1 })" :blank="t('billing.providers.anyCountry')" />
      <CurrencySelect v-model="r.currency" :aria-label="t('billing.providers.currency', { n: i + 1 })" :blank="t('billing.providers.anyCurrency')" />
      <NativeSelect v-model="r.provider" :aria-label="t('billing.providers.ruleProvider', { n: i + 1 })">
        <option v-for="p in ROUTABLE_PROVIDERS" :key="p.name" :value="p.name">{{ p.name }}</option>
      </NativeSelect>
      <Button type="button" variant="ghost" size="icon" :aria-label="t('billing.common.remove')" @click="rules.splice(i, 1)"><X class="size-4" /></Button>
    </div>
    <p v-if="rules.length === 0 && !stored.isPending.value" class="text-muted-foreground text-sm">{{ t("billing.providers.noRules") }}</p>
    <div class="flex flex-wrap gap-2">
      <Button type="button" variant="outline" @click="rules.push({ country: '', currency: '', provider: ROUTABLE_PROVIDERS[0]?.name ?? '' })"><Plus class="size-4" />{{ t("billing.providers.addRule") }}</Button>
      <Button type="button" :disabled="setRules.isPending.value" @click="setRules.mutate(rules.map((r) => ({ ...r, country: r.country.trim().toUpperCase() })))">{{ t("billing.providers.saveRules") }}</Button>
    </div>
    <ErrorText :error="setRules.error.value" />
    <p v-if="setRules.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.providers.rulesSaved") }}</p>

    <form class="flex flex-wrap items-center gap-3 border-t pt-4" role="form" :aria-label="t('billing.providers.preview')" @submit.prevent="preview.mutate({ country: pCountry.trim().toUpperCase(), currency: pCurrency })">
      <h3 class="text-base font-semibold">{{ t("billing.providers.preview") }}</h3>
      <CountrySelect v-model="pCountry" class="w-48" code-only :aria-label="t('billing.providers.previewCountry')" :blank="t('billing.providers.anyCountry')" />
      <CurrencySelect v-model="pCurrency" :aria-label="t('billing.providers.previewCurrency')" :blank="t('billing.providers.anyCurrency')" />
      <Button type="submit" variant="outline">{{ t("billing.providers.previewRun") }}</Button>
    </form>
    <p v-if="preview.data.value" class="text-sm" role="status">
      <template v-if="preview.data.value.provider">
        {{ t("billing.providers.goesTo", { provider: preview.data.value.provider, n: (preview.data.value.rule_index ?? 0) + 1 }) }}
        <span v-if="preview.data.value.can_charge === false" class="text-destructive"> {{ t("billing.providers.cannotCharge") }}</span>
      </template>
      <template v-else>{{ t("billing.providers.goesToDefault") }}</template>
    </p>
  </Card>

  <CatalogueCard :tenant-id="tenantId" />
</template>
