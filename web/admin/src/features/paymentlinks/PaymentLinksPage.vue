<script setup lang="ts">
import { Plus } from "@lucide/vue";
import { useParams, useSearch } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { useTenantCurrency } from "@/features/tenants/currency";
import { matchesSearch } from "@/shared/lib/paging";
import { newKey } from "@/shared/lib/key";
import { useSeededRef } from "@/shared/lib/seeded";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { usePaymentLinkActions, usePaymentLinks } from "./api";
import { absoluteUrl, statusVariant, toRequest, type LinkForm } from "./lib";
import { CurrencySelect } from "@/shared/ui/search-select";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";

const { t, locale } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));

// A page can be opened from an invoice or a customer with the form prefilled (?invoice=…, ?customer=…), and follows the address when it changes in the page.
const search = useSearch({ strict: false });
const prefill = () => ({ invoice: String(search.value.invoice ?? ""), customer: String(search.value.customer ?? "") });
const form = ref<LinkForm>({
  kind: prefill().invoice ? "invoice" : "adhoc",
  invoiceId: prefill().invoice,
  amount: "",
  currency: FALLBACK_CURRENCY,
  description: "",
  customer: prefill().customer,
  promotion: "",
  singleUse: true,
  days: "30",
  key: newKey(),
});
const formError = ref("");
// An ad hoc link is in the tenant's own currency unless the person picks another one.
const seededCurrency = useSeededRef<string>(useTenantCurrency(tenantId), FALLBACK_CURRENCY);
watch(seededCurrency, (c) => (form.value.currency = c), { immediate: true });
watch(prefill, (p) => {
  form.value = { ...form.value, kind: p.invoice ? "invoice" : form.value.kind, invoiceId: p.invoice || form.value.invoiceId, customer: p.customer || form.value.customer };
});

// The form opens on its own when the page was reached from an invoice or a customer with the form prefilled.
const open = ref(prefill().invoice !== "" || prefill().customer !== "");
watch(open, (v) => {
  if (!v) return;
  formError.value = "";
  act.create.reset();
});

const kind = ref("");
const status = ref("");
const pageToken = ref("");
const history = ref<string[]>([]);
const links = usePaymentLinks(tenantId, kind, status, pageToken);
const act = usePaymentLinkActions(tenantId);
const list = computed(() => links.data.value?.links ?? []);
// The API pages by token and has no text search, so the search box narrows the page that is on screen.
const query = ref("");
const shown = computed(() => list.value.filter((l) => matchesSearch([l.id, l.description, l.customer_external_id, l.invoice_id, l.promotion_code, l.amount_minor], query.value)));

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
  act.create.mutate(r.body, { onSuccess: () => (form.value = { ...form.value, key: newKey(), amount: "", description: "", promotion: "" }) });
}
function revoke(id: string | undefined) {
  if (id && window.confirm(t("billing.paymentLinks.confirmRevoke"))) act.revoke.mutate(id);
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
watch([kind, status], filter);
const active = computed(() => kind.value !== "" || status.value !== "" || query.value !== "");
function clear() {
  kind.value = status.value = query.value = "";
}
const when = (v?: string | null) => fmt.date(v, "short", "—");
const what = (l: { kind?: string; amount_minor?: string; currency?: string; invoice_id?: string }) =>
  l.kind !== "invoice" ? formatMinor(l.amount_minor, l.currency, locale.value) : (l.invoice_id ?? "");
</script>

<template>
  <Card class="p-3">
    <div class="px-3 pt-2">
      <h2 class="text-lg font-semibold">{{ t("billing.paymentLinks.list") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.paymentLinks.hint") }}</p>
    </div>
    <DataToolbar v-model:search="query" :active="active" class="pt-3" @clear="clear">
      <NativeSelect v-model="kind" class="w-40" :aria-label="t('billing.paymentLinks.kind')">
        <option value="">{{ t("billing.paymentLinks.anyKind") }}</option>
        <option value="adhoc">{{ t("billing.paymentLinks.kinds.adhoc") }}</option>
        <option value="invoice">{{ t("billing.paymentLinks.kinds.invoice") }}</option>
        <option value="credit_pack">{{ t("billing.paymentLinks.kinds.credit_pack") }}</option>
      </NativeSelect>
      <NativeSelect v-model="status" class="w-40" :aria-label="t('billing.paymentLinks.status')">
        <option value="">{{ t("billing.paymentLinks.anyStatus") }}</option>
        <option v-for="s in ['active', 'paid', 'expired', 'revoked']" :key="s" :value="s">{{ t(`billing.paymentLinks.statuses.${s}`) }}</option>
      </NativeSelect>
      <template #actions>
        <Button type="button" @click="open = true"><Plus class="size-4" aria-hidden="true" />{{ t("billing.paymentLinks.create") }}</Button>
      </template>
    </DataToolbar>
    <ErrorText :error="links.error.value" class="px-3" />
    <ErrorText :error="act.revoke.error.value" class="px-3" />
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
        <TableRow v-for="l in shown" :key="l.id">
          <TableCell>{{ t(`billing.paymentLinks.kinds.${l.kind ?? 'adhoc'}`) }}<span v-if="l.single_use" class="text-muted-foreground block text-xs">{{ t("billing.paymentLinks.singleUseShort") }}</span></TableCell>
          <TableCell><Badge :variant="statusVariant(l.status)">{{ t(`billing.paymentLinks.statuses.${l.status ?? 'active'}`) }}</Badge></TableCell>
          <TableCell>
            <span :class="l.kind === 'invoice' ? 'mono text-xs break-all' : 'tabular-nums'">{{ what(l) }}</span>
            <span v-if="l.description" class="text-muted-foreground block text-xs">{{ l.description }}</span>
            <span v-if="l.promotion_code" class="text-muted-foreground mono block text-xs">{{ t("billing.paymentLinks.promotionCode") }}: {{ l.promotion_code }}</span>
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
    <DataPagination :page="history.length + 1" :has-prev="history.length > 0" :has-next="!!links.data.value?.next_page_token" @prev="back" @next="next" />

    <Sheet v-model:open="open">
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{{ t("billing.paymentLinks.title") }}</SheetTitle>
          <SheetDescription>{{ t("billing.paymentLinks.hint") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form id="payment-link-form" class="grid gap-4" role="form" :aria-label="t('billing.paymentLinks.formLabel')" @submit.prevent="submit">
            <NativeSelect v-model="form.kind" :aria-label="t('billing.paymentLinks.kind')">
              <option value="adhoc">{{ t("billing.paymentLinks.kinds.adhoc") }}</option>
              <option value="invoice">{{ t("billing.paymentLinks.kinds.invoice") }}</option>
            </NativeSelect>
            <template v-if="form.kind === 'invoice'">
              <Input v-model="form.invoiceId" :aria-label="t('billing.paymentLinks.invoiceId')" :placeholder="t('billing.paymentLinks.invoiceId')" />
            </template>
            <template v-else>
              <Input v-model="form.amount" inputmode="decimal" :aria-label="t('billing.paymentLinks.amount')" :placeholder="t('billing.paymentLinks.amount')" />
              <CurrencySelect v-model="form.currency" :aria-label="t('billing.paymentLinks.currency')" />
              <Input v-model="form.description" maxlength="200" :aria-label="t('billing.paymentLinks.description')" :placeholder="t('billing.paymentLinks.description')" />
              <Input v-model="form.customer" :aria-label="t('billing.paymentLinks.customer')" :placeholder="t('billing.paymentLinks.customerOptional')" />
              <Input v-model="form.promotion" maxlength="40" :aria-label="t('billing.paymentLinks.promotionCode')" :placeholder="t('billing.paymentLinks.promotionCode')" :title="t('billing.paymentLinks.promotionHint')" />
            </template>
            <Input v-model="form.days" inputmode="numeric" :aria-label="t('billing.paymentLinks.days')" :placeholder="t('billing.paymentLinks.days')" />
            <label class="flex items-center gap-2 text-sm"><input v-model="form.singleUse" type="checkbox" class="size-4" />{{ t("billing.paymentLinks.singleUse") }}</label>
            <p class="text-muted-foreground text-xs">{{ t(form.kind === "adhoc" ? "billing.paymentLinks.adhocHint" : "billing.paymentLinks.invoiceHint") }}</p>
            <ErrorText :message="formError" />
            <ErrorText :error="act.create.error.value" />
          </form>
          <div v-if="created && act.create.isSuccess.value" class="border-warning/50 bg-warning/10 grid gap-2 rounded-md border p-3" role="status">
            <template v-if="shownUrl">
              <p class="text-sm font-medium">{{ t("billing.paymentLinks.shownOnce") }}</p>
              <CopyField :value="shownUrl" :label="t('billing.paymentLinks.url')" :copy-label="t('billing.paymentLinks.copy')" testid="payment-link-url" />
            </template>
            <p v-else class="text-sm">{{ t("billing.paymentLinks.replayed") }}</p>
          </div>
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="payment-link-form" :disabled="act.create.isPending.value">{{ t("billing.paymentLinks.create") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  </Card>
</template>
