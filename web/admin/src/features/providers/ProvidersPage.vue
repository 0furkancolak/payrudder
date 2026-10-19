<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { Plus, X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { COUNTRY_HINTS, CURRENCIES, PROVIDERS } from "@/shared/lib/currencies";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Textarea } from "@/shared/ui/textarea";
import { useAccountActions, usePreviewRouting, useProviderAccounts, useProviderSubscriptions, useRoutingRules, useSetRoutingRules, type Rule } from "./api";
import { templateText } from "./templates";

const { t, locale } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));

// --- accounts
const accounts = useProviderAccounts(tenantId);
const actions = useAccountActions(tenantId);
const provider = ref("");
const environment = ref<"sandbox" | "live">("live");
const makeDefault = ref(true);
const settings = ref("");
const settingsError = ref("");
watch(provider, (p) => (settings.value = p ? templateText(p) : ""));
function saveAccount() {
  settingsError.value = "";
  let parsed: Record<string, unknown>;
  try {
    parsed = JSON.parse(settings.value) as Record<string, unknown>;
  } catch {
    settingsError.value = t("billing.providers.badJson");
    return;
  }
  actions.save.mutate(
    { provider: provider.value, environment: environment.value, makeDefault: makeDefault.value, settings: parsed },
    { onSuccess: () => (settings.value = templateText(provider.value)) },
  );
}
function removeAccount(p: string, env: "sandbox" | "live") {
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
const when = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium" }).format(new Date(v)) : "—");
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.providers.accounts") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.providers.accountsHint") }}</p>
    <p v-if="accounts.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(accounts.error.value).message }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.providers.provider") }}</TableHead>
          <TableHead>{{ t("billing.providers.environment") }}</TableHead>
          <TableHead>{{ t("billing.providers.default") }}</TableHead>
          <TableHead>{{ t("billing.providers.updated") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="a in accounts.data.value?.accounts ?? []" :key="`${a.provider}-${a.environment}`">
          <TableCell class="font-medium">{{ a.provider }}</TableCell>
          <TableCell>{{ a.environment }}</TableCell>
          <TableCell><Badge v-if="a.is_default" variant="success">{{ t("billing.providers.default") }}</Badge></TableCell>
          <TableCell>{{ when(a.updated_at) }}</TableCell>
          <TableCell class="text-end">
            <Button size="sm" variant="ghost" @click="removeAccount(a.provider ?? '', (a.environment ?? 'live') as 'sandbox' | 'live')">{{ t("billing.common.remove") }}</Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!accounts.isPending.value && (accounts.data.value?.accounts ?? []).length === 0" class="text-muted-foreground text-center text-sm">{{ t("billing.providers.noAccounts") }}</p>

    <form class="grid gap-3" role="form" :aria-label="t('billing.providers.addAccount')" @submit.prevent="saveAccount">
      <h3 class="pt-2 text-base font-semibold">{{ t("billing.providers.addAccount") }}</h3>
      <div class="grid gap-3 sm:grid-cols-3">
        <NativeSelect v-model="provider" :aria-label="t('billing.providers.provider')" required>
          <option value="" disabled>{{ t("billing.providers.provider") }}</option>
          <option v-for="p in PROVIDERS" :key="p.name" :value="p.name">{{ p.name }}</option>
        </NativeSelect>
        <NativeSelect v-model="environment" :aria-label="t('billing.providers.environment')">
          <option value="live">live</option>
          <option value="sandbox">sandbox</option>
        </NativeSelect>
        <label class="flex items-center gap-2 text-sm"><Checkbox v-model="makeDefault" :aria-label="t('billing.providers.makeDefault')" />{{ t("billing.providers.makeDefault") }}</label>
      </div>
      <Textarea v-model="settings" rows="10" class="mono text-xs" spellcheck="false" :aria-label="t('billing.providers.settings')" :placeholder="t('billing.providers.settings')" required />
      <p class="text-muted-foreground text-xs">{{ t("billing.providers.webhookHint", { path: webhookPath }) }}</p>
      <div><Button type="submit" :disabled="actions.save.isPending.value || provider === ''">{{ t("billing.providers.saveAccount") }}</Button></div>
    </form>
    <p v-if="settingsError" class="text-destructive text-sm" role="alert">{{ settingsError }}</p>
    <p v-if="actions.save.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(actions.save.error.value).message }}</p>
    <p v-if="actions.remove.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(actions.remove.error.value).message }}</p>
    <p v-if="actions.save.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.providers.saved") }}</p>
  </Card>

  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.providers.routing") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.providers.routingHint") }}</p>
    <p v-if="stored.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(stored.error.value).message }}</p>
    <datalist id="countries"><option v-for="c in COUNTRY_HINTS" :key="c" :value="c" /></datalist>
    <div v-for="(r, i) in rules" :key="i" class="grid grid-cols-[1fr_1fr_1fr_auto] items-center gap-3" role="group" :aria-label="t('billing.providers.rule', { n: i + 1 })">
      <Input v-model="r.country" list="countries" maxlength="2" :aria-label="t('billing.providers.country', { n: i + 1 })" :placeholder="t('billing.providers.anyCountry')" />
      <NativeSelect v-model="r.currency" :aria-label="t('billing.providers.currency', { n: i + 1 })">
        <option value="">{{ t("billing.providers.anyCurrency") }}</option>
        <option v-for="c in CURRENCIES" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
      <NativeSelect v-model="r.provider" :aria-label="t('billing.providers.ruleProvider', { n: i + 1 })">
        <option v-for="p in PROVIDERS" :key="p.name" :value="p.name">{{ p.name }}</option>
      </NativeSelect>
      <Button type="button" variant="ghost" size="icon" :aria-label="t('billing.common.remove')" @click="rules.splice(i, 1)"><X class="size-4" /></Button>
    </div>
    <p v-if="rules.length === 0 && !stored.isPending.value" class="text-muted-foreground text-sm">{{ t("billing.providers.noRules") }}</p>
    <div class="flex flex-wrap gap-2">
      <Button type="button" variant="outline" @click="rules.push({ country: '', currency: '', provider: PROVIDERS[0].name })"><Plus class="size-4" />{{ t("billing.providers.addRule") }}</Button>
      <Button type="button" :disabled="setRules.isPending.value" @click="setRules.mutate(rules.map((r) => ({ ...r, country: r.country.trim().toUpperCase() })))">{{ t("billing.providers.saveRules") }}</Button>
    </div>
    <p v-if="setRules.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(setRules.error.value).message }}</p>
    <p v-if="setRules.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.providers.rulesSaved") }}</p>

    <form class="flex flex-wrap items-center gap-3 border-t pt-4" role="form" :aria-label="t('billing.providers.preview')" @submit.prevent="preview.mutate({ country: pCountry.trim().toUpperCase(), currency: pCurrency })">
      <h3 class="text-base font-semibold">{{ t("billing.providers.preview") }}</h3>
      <Input v-model="pCountry" class="w-32" maxlength="2" :aria-label="t('billing.providers.previewCountry')" :placeholder="t('billing.providers.previewCountry')" />
      <NativeSelect v-model="pCurrency" :aria-label="t('billing.providers.previewCurrency')">
        <option value="">{{ t("billing.providers.anyCurrency") }}</option>
        <option v-for="c in CURRENCIES" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
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

  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.providers.runByProviders") }}</h2>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.providers.provider") }}</TableHead>
          <TableHead>{{ t("billing.providers.reference") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
          <TableHead>{{ t("billing.providers.nextBilled") }}</TableHead>
          <TableHead>{{ t("billing.providers.updated") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="s in providerSubs.data.value?.subscriptions ?? []" :key="`${s.provider}-${s.provider_ref}`">
          <TableCell>{{ s.provider }} <span class="text-muted-foreground text-xs">{{ s.environment }}</span></TableCell>
          <TableCell class="mono text-xs">{{ s.provider_ref }}</TableCell>
          <TableCell><Badge variant="secondary">{{ s.status }}</Badge></TableCell>
          <TableCell>{{ when(s.next_billed_at) }}</TableCell>
          <TableCell>{{ when(s.updated_at) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!providerSubs.isPending.value && (providerSubs.data.value?.subscriptions ?? []).length === 0" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.providers.noProviderSubscriptions") }}
    </p>
  </Card>
</template>
