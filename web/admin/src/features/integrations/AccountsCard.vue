<script setup lang="ts">
import { Plus, Trash2, FlaskConical, Star } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { useLiveProviders } from "@/features/product/api";
import type { LiveProviderName } from "@/features/product/liveProvider";
import { useDisconnectProvider, useLiveRouting, useMakeDefaultAccount, useSetLiveRouting, type RoutingRule } from "@/features/providers/connectApi";
import { splitAccountKey, type AppRow } from "@/features/providers/connections";
import ProviderSheet from "@/features/providers/ProviderSheet.vue";
import { Button } from "@/shared/ui/button";
import { DataToolbar } from "@/shared/ui/data-table";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { StatusPill } from "@/shared/ui/status-pill";
import { useTableState } from "@/shared/lib/useTableState";
import { ACCOUNT_KINDS, accountEntries, eInvoiceAccounts, type AccountEntry, type AccountKind } from "./accounts";
import { useEInvoiceActions, useEInvoiceIntegration, useEInvoiceRouting, useMailActions, useMailIntegration, useSetEInvoiceRouting, type EInvoiceProvider } from "./api";
import { BRANDS, type IntegrationId } from "./brands";
import { stateTone } from "./status";

/**
 * Every account of an app in one list, whatever it is for: the payment accounts (several at one provider, a Paddle account per region, an iyzico account per
 * company), the e-invoice accounts (the same) and the e-mail sender in use. A row says what it is, which one is the default, and whether it works; the default
 * moves with one click and an account is removed from its row. "Add integration" walks from the kind to the provider and opens the form of that provider. The
 * rules below the list say which account takes which customers: payments by the customer's country and currency, e-invoice documents by the buyer's country.
 */
const props = defineProps<{ appId: string; apps: readonly AppRow[]; isOwner: boolean }>();
const emit = defineEmits<{ einvoice: [provider: EInvoiceProvider, account: string]; mail: [kind: "payrudderMail" | "nafru"] }>();
const { t } = useI18n();

const payments = useLiveProviders(() => props.appId);
const eInvoice = useEInvoiceIntegration(() => props.appId);
const mail = useMailIntegration(() => props.appId);
const eAccounts = computed(() => eInvoiceAccounts(eInvoice.data.value));
const entries = computed(() => accountEntries({ payments: payments.data.value?.accounts, eInvoice: eAccounts.value, mail: mail.data.value }));

const nameOf = (id: string) => BRANDS[id as IntegrationId]?.name ?? t(`product.integrations.names.${id}`);
const entryName = (e: AccountEntry) => (e.kind === "payment" && !BRANDS[e.provider as IntegrationId]?.name ? t(`product.goLive.provider.names.${e.provider}`) : nameOf(e.provider));

// --- the list: search and two filters
const all = computed(() => ({ value: "", label: t("common.all") }));
const table = useTableState<AccountEntry>(entries, {
  searchFields: (e) => [entryName(e), e.key, e.label, e.provider],
  filters: { kind: (e, v) => e.kind === v, state: (e, v) => e.state === v },
});
const kindFilter = computed({ get: () => table.filters.value.kind ?? [], set: (v: string[]) => (table.filters.value.kind = v) });
const stateFilter = computed({ get: () => table.filters.value.state ?? [], set: (v: string[]) => (table.filters.value.state = v) });
const kindOptions = computed(() => [all.value, ...ACCOUNT_KINDS.map((k) => ({ value: k, label: t(`product.integrations.accounts.kinds.${k}`) }))]);
const stateOptions = computed(() => [all.value, ...(["working", "problem", "always", "unused"] as const).map((s) => ({ value: s, label: t(`product.integrations.state.${s}`) }))]);

// --- a row's own actions
const makeDefaultPayment = useMakeDefaultAccount(() => props.appId);
const eActions = useEInvoiceActions(() => props.appId);
const mActions = useMailActions(() => props.appId);
const disconnect = useDisconnectProvider();
const failure = computed(() => makeDefaultPayment.error.value ?? eActions.setDefault.error.value ?? eActions.remove.error.value ?? mActions.remove.error.value ?? disconnect.error.value);
/** What a test of an e-invoice or e-mail account found, by row: an account has no standing health, so a row says what its last test said. */
const tested = ref<Record<string, "working" | "problem">>({});
const stateOf = (e: AccountEntry) => tested.value[e.id] ?? e.state;

function makeDefault(e: AccountEntry) {
  if (e.kind === "payment") makeDefaultPayment.mutate(e.key);
  else if (e.kind === "einvoice") eActions.setDefault.mutate(e.key);
}

async function remove(e: AccountEntry) {
  if (!(await askConfirm({ body: t("product.integrations.accounts.confirmRemove", { name: label(e) }) }))) return;
  if (e.kind === "payment") disconnect.mutate({ apps: props.apps.filter((a) => a.id === props.appId), provider: e.provider as LiveProviderName, key: e.key });
  else if (e.kind === "einvoice") eActions.remove.mutate(e.key);
  else mActions.remove.mutate();
}

function testNow(e: AccountEntry) {
  eActions.test.mutate({ account: e.key, taxId: "" }, { onSuccess: (r) => (tested.value = { ...tested.value, [e.id]: r.ok ? "working" : "problem" }) });
}

function manage(e: AccountEntry) {
  if (e.kind === "payment") {
    const { provider, label: tag } = splitAccountKey(e.key);
    openPayments(provider as LiveProviderName, tag, false);
  } else if (e.kind === "einvoice") emit("einvoice", e.provider as EInvoiceProvider, e.key);
  else emit("mail", e.provider as "payrudderMail" | "nafru");
}

const label = (e: AccountEntry) => (e.label ? `${entryName(e)} (${e.label})` : entryName(e));
const actionsOf = (e: AccountEntry) => {
  const out = [];
  if (e.kind === "einvoice") out.push({ key: "test", label: t("product.integrations.test"), icon: FlaskConical, onSelect: () => testNow(e) });
  if (props.isOwner && (e.kind !== "mail" || e.provider === "nafru")) out.push({ key: "remove", label: t("product.integrations.accounts.remove"), icon: Trash2, destructive: true, onSelect: () => void remove(e) });
  return out;
};
const canMakeDefault = (e: AccountEntry) => props.isOwner && !e.isDefault && (e.kind === "payment" || e.kind === "einvoice") && e.state !== "none";

// --- the payments sheet: a further account is added or managed through the provider's own form
const sheetOpen = ref(false);
const sheetProvider = ref<LiveProviderName | "">("");
const sheetLabel = ref("");
const sheetAdding = ref(false);
function openPayments(provider: LiveProviderName, tag: string, adding: boolean) {
  sheetProvider.value = provider;
  sheetLabel.value = tag;
  sheetAdding.value = adding;
  sheetOpen.value = true;
}

// --- "Add integration": kind, then provider, then that provider's form
const addOpen = ref(false);
const addKind = ref<AccountKind>("payment");
const addProvider = ref("");
const PROVIDERS: Record<AccountKind, readonly string[]> = { payment: ["iyzico", "paddle", "stripe"], einvoice: ["nilvera", "webhook"], mail: ["nafru"] };
watch(addOpen, (v) => {
  if (v) addProvider.value = "";
});
watch(addKind, () => (addProvider.value = ""));
function continueAdd() {
  const p = addProvider.value;
  if (p === "") return;
  addOpen.value = false;
  if (addKind.value === "payment") openPayments(p as LiveProviderName, "", entries.value.some((e) => e.kind === "payment" && e.provider === p));
  else if (addKind.value === "einvoice") emit("einvoice", p as EInvoiceProvider, "");
  else emit("mail", "nafru");
}

// --- payment routing: edited as a list, saved as a whole
const rows = computed(() => entries.value.filter((e) => e.kind === "payment" && e.provider !== "banktransfer"));
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
const accountOptions = computed(() => rows.value.map((r) => ({ key: r.key, label: label(r) })));
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

// --- e-invoice routing: the buyer's country names the account that issues the document
const eRouting = useEInvoiceRouting(() => props.appId);
const setERouting = useSetEInvoiceRouting(() => props.appId);
const eDraft = ref<{ country: string; account: string }[]>([]);
const eSaved = ref(false);
watch(
  () => eRouting.data.value,
  (rules) => {
    eDraft.value = (rules ?? []).map((r) => ({ ...r }));
  },
  { immediate: true },
);
const eDirty = computed(() => JSON.stringify(eDraft.value) !== JSON.stringify(eRouting.data.value ?? []));
const eOptions = computed(() => entries.value.filter((e) => e.kind === "einvoice").map((e) => ({ key: e.key, label: label(e) })));
const eDuplicate = computed(() => {
  const seen = new Set<string>();
  return eDraft.value.some((r) => {
    const c = r.country.trim().toUpperCase();
    if (c === "") return false;
    if (seen.has(c)) return true;
    seen.add(c);
    return false;
  });
});
function addERule() {
  eDraft.value.push({ country: "", account: eOptions.value[0]?.key ?? "" });
}
function saveE() {
  eSaved.value = false;
  setERouting.mutate(
    eDraft.value.filter((r) => r.country.trim() !== "").map((r) => ({ country: r.country.trim().toUpperCase(), account: r.account })),
    { onSuccess: () => (eSaved.value = true) },
  );
}
</script>

<template>
  <section class="surface flex flex-col gap-5 p-5" data-testid="accounts-card">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold tracking-tight">{{ t("product.integrations.accounts.title") }}</h2>
        <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.integrations.accounts.intro") }}</p>
      </div>
      <Button v-if="isOwner" variant="outline" size="sm" data-testid="account-add" @click="addOpen = true"><Plus class="size-4" />{{ t("product.integrations.accounts.add") }}</Button>
    </div>

    <DataToolbar v-model:search="table.search.value" :active="table.active.value" :placeholder="t('product.integrations.accounts.search')" @clear="table.clear()">
      <FilterSelect v-model="kindFilter" multiple :label="t('product.integrations.accounts.kind')" :options="kindOptions" />
      <FilterSelect v-model="stateFilter" multiple :label="t('product.integrations.accounts.status')" :options="stateOptions" />
    </DataToolbar>

    <p v-if="entries.filter((e) => e.kind !== 'mail').length === 0" class="text-muted-foreground text-sm" data-testid="accounts-empty">{{ t("product.providers.account.empty") }}</p>
    <ul class="divide-border flex flex-col divide-y" :aria-label="t('product.integrations.accounts.title')">
      <li v-for="e in table.items.value" :key="e.id" class="flex flex-wrap items-center justify-between gap-3 py-3" :data-testid="`account-${e.id}`" :data-kind="e.kind">
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <span class="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs" :data-testid="`account-kind-${e.id}`">{{ t(`product.integrations.accounts.kinds.${e.kind}`) }}</span>
          <span class="font-medium">{{ entryName(e) }}</span>
          <span v-if="e.label" class="mono bg-muted rounded-md px-1.5 py-0.5 text-xs">{{ e.label }}</span>
          <span v-if="e.mode" class="text-muted-foreground text-xs">{{ t(`product.integrations.einvoice.modes.${e.mode}`) }}</span>
          <StatusPill :tone="stateTone(stateOf(e))" :data-testid="`account-state-${e.id}`">{{ t(`product.integrations.state.${stateOf(e)}`) }}</StatusPill>
          <StatusPill v-if="e.isDefault" tone="info" :data-testid="`account-default-${e.id}`">{{ e.kind === 'mail' ? t("product.integrations.accounts.inUse") : t("product.providers.account.default") }}</StatusPill>
        </div>
        <div class="flex items-center gap-2">
          <Button v-if="canMakeDefault(e)" variant="ghost" size="sm" :disabled="makeDefaultPayment.isPending.value || eActions.setDefault.isPending.value" :data-testid="`account-make-default-${e.id}`" @click="makeDefault(e)">
            <Star class="size-4" />{{ t("product.providers.account.makeDefault") }}
          </Button>
          <Button variant="outline" size="sm" :disabled="!isOwner && e.kind !== 'mail'" :data-testid="`account-manage-${e.id}`" @click="manage(e)">{{ e.kind === 'mail' && e.provider === 'payrudderMail' ? t("product.integrations.settings") : t("product.providers.manage") }}</Button>
          <RowActions v-if="actionsOf(e).length > 0" :actions="actionsOf(e)" />
        </div>
      </li>
    </ul>
    <p v-if="table.total.value === 0 && table.active.value" class="text-muted-foreground text-sm">{{ t("common.noMatches") }}</p>
    <ErrorText :error="failure" />

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

    <div v-if="eOptions.length > 1 || eDraft.length > 0" class="flex flex-col gap-3" data-testid="einvoice-routing-editor">
      <div>
        <h3 class="text-sm font-semibold">{{ t("product.integrations.accounts.eRouting.title") }}</h3>
        <p class="text-muted-foreground text-xs">{{ t("product.integrations.accounts.eRouting.intro") }}</p>
      </div>
      <ul v-if="eDraft.length > 0" class="flex flex-col gap-2">
        <li v-for="(rule, i) in eDraft" :key="i" class="grid items-end gap-2 sm:grid-cols-[6rem_1fr_auto]" :data-testid="`einvoice-rule-${i}`">
          <label class="grid gap-1 text-xs">{{ t("product.providers.account.routing.country") }}
            <Input v-model="rule.country" class="mono uppercase" maxlength="2" :disabled="!isOwner" :data-testid="`einvoice-rule-country-${i}`" />
          </label>
          <label class="grid gap-1 text-xs">{{ t("product.providers.account.routing.account") }}
            <NativeSelect v-model="rule.account" :disabled="!isOwner" :data-testid="`einvoice-rule-account-${i}`">
              <option v-for="o in eOptions" :key="o.key" :value="o.key">{{ o.label }}</option>
            </NativeSelect>
          </label>
          <Button v-if="isOwner" variant="ghost" size="sm" :data-testid="`einvoice-rule-remove-${i}`" @click="eDraft.splice(i, 1)">{{ t("product.providers.account.routing.remove") }}</Button>
        </li>
      </ul>
      <p v-if="eDuplicate" class="text-destructive text-xs" role="alert">{{ t("product.integrations.accounts.eRouting.duplicate") }}</p>
      <div v-if="isOwner" class="flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" type="button" data-testid="einvoice-rule-add" @click="addERule">{{ t("product.providers.account.routing.addRule") }}</Button>
        <Button size="sm" type="button" :disabled="!eDirty || eDuplicate || setERouting.isPending.value" data-testid="einvoice-rule-save" @click="saveE">{{ t("product.providers.account.routing.save") }}</Button>
        <span v-if="eSaved && !eDirty" class="text-success text-xs" role="status" data-testid="einvoice-rule-saved">{{ t("product.providers.account.routing.saved") }}</span>
      </div>
      <ErrorText :error="setERouting.error.value" />
    </div>

    <Sheet v-model:open="addOpen" name="add-integration">
      <SheetContent class="sm:max-w-md" data-testid="add-integration-sheet">
        <SheetHeader>
          <SheetTitle>{{ t("product.integrations.accounts.add") }}</SheetTitle>
          <SheetDescription>{{ t("product.integrations.accounts.addIntro") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form id="add-integration-form" class="grid gap-3" @submit.prevent="continueAdd">
            <label class="grid gap-1 text-sm">{{ t("product.integrations.accounts.kind") }}
              <NativeSelect v-model="addKind" data-testid="add-kind">
                <option v-for="k in ACCOUNT_KINDS" :key="k" :value="k">{{ t(`product.integrations.accounts.kinds.${k}`) }}</option>
              </NativeSelect>
            </label>
            <label class="grid gap-1 text-sm">{{ t("product.integrations.accounts.provider") }}
              <NativeSelect v-model="addProvider" data-testid="add-provider">
                <option value="" disabled>{{ t("product.integrations.accounts.provider") }}</option>
                <option v-for="p in PROVIDERS[addKind]" :key="p" :value="p">{{ addKind === 'payment' && !BRANDS[p as IntegrationId]?.name ? t(`product.goLive.provider.names.${p}`) : nameOf(p) }}</option>
              </NativeSelect>
            </label>
            <p v-if="addKind === 'mail'" class="text-muted-foreground text-xs">{{ t("product.integrations.accounts.mailNote") }}</p>
          </form>
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" @click="addOpen = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="add-integration-form" :disabled="addProvider === ''" data-testid="add-continue">{{ t("product.integrations.accounts.continue") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>

    <ProviderSheet v-model:open="sheetOpen" :provider="sheetProvider" :tag="sheetLabel" :adding="sheetAdding" :app-id="appId" :apps="apps" scope="app" :can-org="false" :is-owner="isOwner" />
  </section>
</template>
