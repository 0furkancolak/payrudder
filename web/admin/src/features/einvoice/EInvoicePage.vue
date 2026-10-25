<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { useTenant } from "@/features/tenants/api";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useEInvoiceAccountActions, useEInvoiceAccounts, useSaveEInvoiceSettings, type EInvoiceOn, type LegalInfo } from "./api";
import type { Environment } from "@/shared/lib/environment";

type Provider = "mock" | "nilvera" | "webhook";

const { t } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(tenantId);

// --- when an invoice becomes an e-document, and the seller's company data
const save = useSaveEInvoiceSettings(tenantId);
const eInvoiceOn = ref<EInvoiceOn>("off");
const legal = ref<LegalInfo>({});
watch(
  () => tenant.data.value,
  (v) => {
    eInvoiceOn.value = (v?.e_invoice_on as EInvoiceOn | undefined) || "off";
    legal.value = { ...(v?.legal ?? {}) };
  },
  { immediate: true },
);
function saveSettings() {
  const trimmed = Object.fromEntries(Object.entries(legal.value).map(([k, v]) => [k, (v ?? "").trim()]));
  save.mutate({ legal: trimmed, eInvoiceOn: eInvoiceOn.value });
}

// --- the integrator account
const accounts = useEInvoiceAccounts(tenantId);
const actions = useEInvoiceAccountActions(tenantId);
const environment = ref<Environment>("live");
const provider = ref<Provider>("nilvera");
const mode = ref<"test" | "production">("test");
const apiKey = ref("");
const baseUrl = ref("");
const hookUrl = ref("");
const hookSecret = ref("");
/** The sandbox only reaches the mock; a live account may be any integrator. */
const providers = computed<Provider[]>(() => (environment.value === "sandbox" ? ["mock"] : ["nilvera", "webhook", "mock"]));
watch(environment, (e) => {
  if (e === "sandbox") provider.value = "mock";
  else if (provider.value === "mock") provider.value = "nilvera";
});
function credentials(): Record<string, string> {
  if (provider.value === "nilvera") return { api_key: apiKey.value.trim(), ...(baseUrl.value.trim() ? { base_url: baseUrl.value.trim() } : {}) };
  if (provider.value === "webhook") return { url: hookUrl.value.trim(), secret: hookSecret.value };
  return {};
}
function saveAccount() {
  actions.save.mutate(
    { environment: environment.value, provider: provider.value, mode: mode.value, credentials: credentials() },
    { onSuccess: () => (apiKey.value = hookSecret.value = "") },
  );
}
function removeAccount(env: Environment, p: string) {
  if (window.confirm(t("einvoice.settings.confirmRemove", { provider: p, environment: env }))) actions.remove.mutate(env);
}
const when = (v?: string) => fmt.date(v, "day", "—");
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("einvoice.settings.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("einvoice.settings.hint") }}</p>
    <ErrorText :error="tenant.error.value" />
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" role="form" :aria-label="t('einvoice.settings.formLabel')" @submit.prevent="saveSettings">
      <NativeSelect v-model="eInvoiceOn" :aria-label="t('einvoice.settings.on')">
        <option value="off">{{ t("einvoice.settings.onOff") }}</option>
        <option value="finalized">{{ t("einvoice.settings.onFinalized") }}</option>
        <option value="paid">{{ t("einvoice.settings.onPaid") }}</option>
      </NativeSelect>
      <Input v-model="legal.legal_name" maxlength="160" :aria-label="t('einvoice.settings.legalName')" :placeholder="t('einvoice.settings.legalName')" />
      <Input v-model="legal.tax_id" maxlength="40" :aria-label="t('einvoice.settings.taxId')" :placeholder="t('einvoice.settings.taxId')" />
      <Input v-model="legal.tax_office" maxlength="120" :aria-label="t('einvoice.settings.taxOffice')" :placeholder="t('einvoice.settings.taxOffice')" />
      <Input v-model="legal.mersis_no" maxlength="40" :aria-label="t('einvoice.settings.mersis')" :placeholder="t('einvoice.settings.mersis')" />
      <Input v-model="legal.trade_registry_no" maxlength="40" :aria-label="t('einvoice.settings.tradeRegistry')" :placeholder="t('einvoice.settings.tradeRegistry')" />
      <div class="sm:col-span-2 lg:col-span-3"><Button type="submit" :disabled="save.isPending.value">{{ t("einvoice.settings.save") }}</Button></div>
    </form>
    <ErrorText :error="save.error.value" />
    <p v-if="save.isSuccess.value" class="text-success text-sm" role="status">{{ t("einvoice.settings.saved") }}</p>
  </Card>

  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("einvoice.account.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("einvoice.account.hint") }}</p>
    <ErrorText :error="accounts.error.value" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("einvoice.account.provider") }}</TableHead>
          <TableHead>{{ t("einvoice.account.environment") }}</TableHead>
          <TableHead>{{ t("einvoice.account.mode") }}</TableHead>
          <TableHead>{{ t("einvoice.account.credentials") }}</TableHead>
          <TableHead>{{ t("einvoice.account.updated") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="a in accounts.data.value ?? []" :key="a.environment" data-testid="einvoice-account">
          <TableCell class="font-medium">{{ a.provider }}</TableCell>
          <TableCell>{{ a.environment }}</TableCell>
          <TableCell>{{ a.mode ? t(`einvoice.account.modes.${a.mode}`) : "—" }}</TableCell>
          <TableCell><Badge :variant="a.has_credentials ? 'success' : 'secondary'">{{ a.has_credentials ? t("einvoice.account.stored") : t("einvoice.account.none") }}</Badge></TableCell>
          <TableCell>{{ when(a.updated_at) }}</TableCell>
          <TableCell class="text-end"><Button size="sm" variant="ghost" @click="removeAccount((a.environment ?? 'live') as 'sandbox' | 'live', a.provider ?? '')">{{ t("billing.common.remove") }}</Button></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!accounts.isPending.value && (accounts.data.value ?? []).length === 0" class="text-muted-foreground text-center text-sm">{{ t("einvoice.account.empty") }}</p>

    <form class="grid gap-3" role="form" :aria-label="t('einvoice.account.formLabel')" @submit.prevent="saveAccount">
      <h3 class="pt-2 text-base font-semibold">{{ t("einvoice.account.add") }}</h3>
      <div class="grid gap-3 sm:grid-cols-3">
        <NativeSelect v-model="environment" :aria-label="t('einvoice.account.environment')">
          <option value="live">live</option>
          <option value="sandbox">sandbox</option>
        </NativeSelect>
        <NativeSelect v-model="provider" :aria-label="t('einvoice.account.provider')">
          <option v-for="p in providers" :key="p" :value="p">{{ p }}</option>
        </NativeSelect>
        <NativeSelect v-model="mode" :aria-label="t('einvoice.account.mode')">
          <option value="test">{{ t("einvoice.account.modes.test") }}</option>
          <option value="production">{{ t("einvoice.account.modes.production") }}</option>
        </NativeSelect>
      </div>
      <p v-if="environment === 'sandbox'" class="text-muted-foreground text-xs">{{ t("einvoice.account.sandboxHint") }}</p>
      <div v-if="provider === 'nilvera'" class="grid gap-3 sm:grid-cols-2">
        <Input v-model="apiKey" type="password" autocomplete="off" :aria-label="t('einvoice.account.apiKey')" :placeholder="t('einvoice.account.apiKey')" required />
        <Input v-model="baseUrl" type="url" :aria-label="t('einvoice.account.baseUrl')" :placeholder="t('einvoice.account.baseUrl')" />
      </div>
      <div v-else-if="provider === 'webhook'" class="grid gap-3 sm:grid-cols-2">
        <Input v-model="hookUrl" type="url" :aria-label="t('einvoice.account.webhookUrl')" :placeholder="t('einvoice.account.webhookUrl')" required />
        <Input v-model="hookSecret" type="password" autocomplete="off" :aria-label="t('einvoice.account.webhookSecret')" :placeholder="t('einvoice.account.webhookSecret')" required />
      </div>
      <p v-if="provider !== 'mock'" class="text-muted-foreground text-xs">{{ t("einvoice.account.neverShown") }}</p>
      <div><Button type="submit" :disabled="actions.save.isPending.value">{{ t("einvoice.account.save") }}</Button></div>
    </form>
    <ErrorText :error="actions.save.error.value" />
    <ErrorText :error="actions.remove.error.value" />
    <p v-if="actions.save.isSuccess.value" class="text-success text-sm" role="status">{{ t("einvoice.account.saved") }}</p>
  </Card>
</template>
