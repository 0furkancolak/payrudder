<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { toApiError } from "@/shared/api/client";
import { formatMinor } from "@/shared/lib/money";
import { useTenantCurrency } from "@/features/tenants/currency";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { usePaymentLinkActions, usePaymentLinks } from "./api";
import { absoluteUrl, newKey, statusVariant, toRequest, type LinkForm } from "./lib";
import { CurrencySelect } from "@/shared/ui/search-select";

const { t, locale } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));

// A page can be opened from an invoice or a customer with the form prefilled (?invoice=…, ?customer=…).
const query = new URLSearchParams(window.location.search);
const prefillInvoice = query.get("invoice") ?? "";
const form = ref<LinkForm>({
  kind: prefillInvoice ? "invoice" : "adhoc",
  invoiceId: prefillInvoice,
  amount: "",
  currency: "USD",
  description: "",
  customer: query.get("customer") ?? "",
  singleUse: true,
  days: "30",
  key: newKey(),
});
const formError = ref("");
// An ad hoc link is in the tenant's own currency unless the person picks another one.
const tenantCurrency = useTenantCurrency(tenantId);
let currencySeeded = false;
watch(
  tenantCurrency,
  (c) => {
    if (c && !currencySeeded) {
      currencySeeded = true;
      form.value.currency = c;
    }
  },
  { immediate: true },
);

const kind = ref("");
const status = ref("");
const pageToken = ref("");
const history = ref<string[]>([]);
const links = usePaymentLinks(tenantId, kind, status, pageToken);
const act = usePaymentLinkActions(tenantId);
const list = computed(() => links.data.value?.links ?? []);

/** The URL is returned once, right after creation, and is not kept anywhere else. */
const created = computed(() => act.create.data.value as { url?: string; replayed?: boolean } | undefined);
const shownUrl = computed(() => absoluteUrl(created.value?.url, window.location.origin));

function submit() {
  formError.value = "";
  const r = toRequest(form.value);
  if ("error" in r) {
    formError.value = t(`billing.paymentLinks.errors.${r.error}`);
    return;
  }
  act.create.mutate(r.body, { onSuccess: () => (form.value = { ...form.value, key: newKey(), amount: "", description: "" }) });
}
function revoke(id: string | undefined) {
  if (id && window.confirm(t("billing.paymentLinks.confirmRevoke"))) act.revoke.mutate(id);
}
async function copy() {
  try {
    await navigator.clipboard.writeText(shownUrl.value);
  } catch {
    /* the field is selectable */
  }
}
function next() {
  const token = links.data.value?.next_page_token;
  if (!token) return;
  history.value.push(pageToken.value);
  pageToken.value = token;
}
function back() {
  pageToken.value = history.value.pop() ?? "";
}
function filter() {
  pageToken.value = "";
  history.value = [];
}
const when = (v?: string | null) => fmt.date(v, "short", "—");
const what = (l: { kind?: string; amount_minor?: string; currency?: string; invoice_id?: string }) =>
  l.kind !== "invoice" ? formatMinor(l.amount_minor, l.currency, locale.value) : (l.invoice_id ?? "");
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.paymentLinks.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.paymentLinks.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" role="form" :aria-label="t('billing.paymentLinks.formLabel')" @submit.prevent="submit">
      <NativeSelect v-model="form.kind" :aria-label="t('billing.paymentLinks.kind')">
        <option value="adhoc">{{ t("billing.paymentLinks.kinds.adhoc") }}</option>
        <option value="invoice">{{ t("billing.paymentLinks.kinds.invoice") }}</option>
      </NativeSelect>
      <template v-if="form.kind === 'invoice'">
        <Input v-model="form.invoiceId" class="sm:col-span-2" :aria-label="t('billing.paymentLinks.invoiceId')" :placeholder="t('billing.paymentLinks.invoiceId')" />
      </template>
      <template v-else>
        <Input v-model="form.amount" inputmode="decimal" :aria-label="t('billing.paymentLinks.amount')" :placeholder="t('billing.paymentLinks.amount')" />
        <CurrencySelect v-model="form.currency" :aria-label="t('billing.paymentLinks.currency')" />
        <Input v-model="form.description" maxlength="200" class="sm:col-span-2" :aria-label="t('billing.paymentLinks.description')" :placeholder="t('billing.paymentLinks.description')" />
        <Input v-model="form.customer" :aria-label="t('billing.paymentLinks.customer')" :placeholder="t('billing.paymentLinks.customerOptional')" />
      </template>
      <Input v-model="form.days" inputmode="numeric" :aria-label="t('billing.paymentLinks.days')" :placeholder="t('billing.paymentLinks.days')" />
      <label class="flex items-center gap-2 text-sm"><input v-model="form.singleUse" type="checkbox" class="size-4" />{{ t("billing.paymentLinks.singleUse") }}</label>
      <Button type="submit" class="sm:col-span-2" :disabled="act.create.isPending.value">{{ t("billing.paymentLinks.create") }}</Button>
    </form>
    <p class="text-muted-foreground text-xs">{{ t(form.kind === "adhoc" ? "billing.paymentLinks.adhocHint" : "billing.paymentLinks.invoiceHint") }}</p>
    <p v-if="formError" class="text-destructive text-sm" role="alert">{{ formError }}</p>
    <p v-if="act.create.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(act.create.error.value).message }}</p>
    <div v-if="created && act.create.isSuccess.value" class="border-warning/50 bg-warning/10 grid gap-2 rounded-md border p-3" role="status">
      <template v-if="shownUrl">
        <p class="text-sm font-medium">{{ t("billing.paymentLinks.shownOnce") }}</p>
        <div class="flex flex-wrap items-center gap-2">
          <Input :model-value="shownUrl" readonly class="mono max-w-xl" :aria-label="t('billing.paymentLinks.url')" data-testid="payment-link-url" @focus="($event.target as HTMLInputElement).select()" />
          <Button type="button" variant="outline" @click="copy">{{ t("billing.paymentLinks.copy") }}</Button>
        </div>
      </template>
      <p v-else class="text-sm">{{ t("billing.paymentLinks.replayed") }}</p>
    </div>
  </Card>

  <Card class="p-3">
    <div class="flex flex-wrap items-center gap-3 px-3 pt-2">
      <h2 class="text-lg font-semibold">{{ t("billing.paymentLinks.list") }}</h2>
      <NativeSelect v-model="kind" class="w-40" :aria-label="t('billing.paymentLinks.kind')" @change="filter">
        <option value="">{{ t("billing.paymentLinks.anyKind") }}</option>
        <option value="adhoc">{{ t("billing.paymentLinks.kinds.adhoc") }}</option>
        <option value="invoice">{{ t("billing.paymentLinks.kinds.invoice") }}</option>
        <option value="credit_pack">{{ t("billing.paymentLinks.kinds.credit_pack") }}</option>
      </NativeSelect>
      <NativeSelect v-model="status" class="w-40" :aria-label="t('billing.paymentLinks.status')" @change="filter">
        <option value="">{{ t("billing.paymentLinks.anyStatus") }}</option>
        <option v-for="s in ['active', 'paid', 'expired', 'revoked']" :key="s" :value="s">{{ t(`billing.paymentLinks.statuses.${s}`) }}</option>
      </NativeSelect>
    </div>
    <p v-if="links.error.value" class="text-destructive px-3 text-sm" role="alert">{{ toApiError(links.error.value).message }}</p>
    <p v-if="act.revoke.error.value" class="text-destructive px-3 text-sm" role="alert">{{ toApiError(act.revoke.error.value).message }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.paymentLinks.kind") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.status") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.for") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.customer") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.uses") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.expires") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.createdAt") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.webhooks.actions") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="l in list" :key="l.id">
          <TableCell>{{ t(`billing.paymentLinks.kinds.${l.kind ?? 'adhoc'}`) }}<span v-if="l.single_use" class="text-muted-foreground block text-xs">{{ t("billing.paymentLinks.singleUseShort") }}</span></TableCell>
          <TableCell><Badge :variant="statusVariant(l.status)">{{ t(`billing.paymentLinks.statuses.${l.status ?? 'active'}`) }}</Badge></TableCell>
          <TableCell>
            <span :class="l.kind === 'invoice' ? 'mono text-xs break-all' : 'tabular-nums'">{{ what(l) }}</span>
            <span v-if="l.description" class="text-muted-foreground block text-xs">{{ l.description }}</span>
          </TableCell>
          <TableCell>{{ l.customer_external_id || "—" }}</TableCell>
          <TableCell class="text-xs tabular-nums">{{ t("billing.paymentLinks.usesValue", { used: l.use_count ?? 0, paid: l.paid_count ?? 0 }) }}</TableCell>
          <TableCell class="text-xs">{{ when(l.expire_time) }}</TableCell>
          <TableCell class="text-xs">{{ when(l.create_time) }}</TableCell>
          <TableCell class="text-end whitespace-nowrap">
            <Button v-if="l.status === 'active' || l.status === 'paid'" size="sm" variant="destructive" :disabled="act.revoke.isPending.value" @click="revoke(l.id)">{{ t("billing.paymentLinks.revoke") }}</Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!links.isPending.value && list.length === 0 && !links.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.paymentLinks.empty") }}</p>
    <div class="flex justify-end gap-2 px-3 pb-2">
      <Button size="sm" variant="outline" :disabled="history.length === 0" @click="back">{{ t("billing.paymentLinks.previous") }}</Button>
      <Button size="sm" variant="outline" :disabled="!links.data.value?.next_page_token" @click="next">{{ t("billing.paymentLinks.next") }}</Button>
    </div>
  </Card>
</template>
