<script setup lang="ts">
import { ArrowUpRight, Ban, Plus } from "@lucide/vue";
import AppField from "@/app/layout/AppField.vue";
import { useParams, useSearch } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { useTenantCurrency } from "@/features/tenants/currency";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { matchesSearch } from "@/shared/lib/paging";
import { PaymentContext } from "@/features/related";
import { useEdgeDetail } from "@/shared/lib/useEdgeDetail";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { newKey } from "@/shared/lib/key";
import { useSeededRef } from "@/shared/lib/seeded";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Input } from "@/shared/ui/input";
import { RowActions } from "@/shared/ui/row-actions";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { usePaymentLinkActions, usePaymentLinks } from "./api";
import { absoluteUrl, toRequest, type LinkForm } from "./lib";
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
const range = ref<DateRange>(thisMonth());
const shown = computed(() => list.value.filter((l) => matchesSearch([l.id, l.description, l.customer_external_id, l.invoice_id, l.promotion_code, l.amount_minor], query.value) && inRange(l.create_time, range.value)));
const kinds = computed(() => [{ value: "", label: t("billing.paymentLinks.anyKind") }, ...["adhoc", "invoice", "credit_pack"].map((k) => ({ value: k, label: t(`billing.paymentLinks.kinds.${k}`) }))]);
const statuses = computed(() => [{ value: "", label: t("billing.paymentLinks.anyStatus") }, ...["active", "paid", "expired", "revoked"].map((s) => ({ value: s, label: t(`billing.paymentLinks.statuses.${s}`) }))]);

// The link whose detail is open, as its place among the links on screen; the arrows go on to the next and previous page of links.
const nav = useEdgeDetail(
  shown,
  { hasPrev: () => history.value.length > 0, hasNext: () => !!links.data.value?.next_page_token, prev: back, next },
  {
    kind: "payment-link",
    id: (l) => l.id ?? "",
    ready: () => links.isSuccess.value,
    rewind: filter,
    // The search box and the date range hide links of the page that is on screen: the address wins.
    missing: (id) => {
      if (!list.value.some((l) => l.id === id) || (query.value === "" && range.value.from === "" && range.value.to === "")) return false;
      query.value = "";
      range.value = { from: "", to: "" };
      return true;
    },
  },
);
const detail = nav.index;
const current = nav.row;
// A link from an invoice or a customer opens the payment link it names, when it is on the page of links loaded.
useOpenParam("link", () => links.isSuccess.value, (id) => {
  const at = shown.value.findIndex((l) => l.id === id);
  if (at >= 0) detail.value = at;
});
const canRevoke = (l: { status?: string } | undefined) => l?.status === "active" || l?.status === "paid";

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
const active = computed(() => kind.value !== "" || status.value !== "" || query.value !== "" || range.value.from !== "" || range.value.to !== "");
function clear() {
  kind.value = status.value = query.value = "";
  range.value = { from: "", to: "" };
}
const when = (v?: string | null) => fmt.date(v, "short", "—");
const usesOf = (l: { use_count?: number; paid_count?: number }) => t("billing.paymentLinks.usesValue", { used: l.use_count ?? 0, paid: l.paid_count ?? 0 });
const rowActions = (l: { id?: string; status?: string }, i: number) => [
  { key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => (detail.value = i) },
  ...(canRevoke(l) ? [{ key: "revoke", label: t("billing.paymentLinks.revoke"), icon: Ban, destructive: true, separatorBefore: true, disabled: act.revoke.isPending.value, onSelect: () => revoke(l.id) }] : []),
];
const detailRows = computed(() => {
  const l = current.value;
  if (!l) return [];
  return [
    { key: "status", label: t("billing.paymentLinks.status") },
    { key: "kind", label: t("billing.paymentLinks.kind"), value: t(`billing.paymentLinks.kinds.${l.kind ?? "adhoc"}`) + (l.single_use ? ` · ${t("billing.paymentLinks.singleUseShort")}` : "") },
    { key: "for", label: t("billing.paymentLinks.for"), value: what(l) },
    { key: "description", label: t("billing.paymentLinks.description"), value: l.description },
    { key: "promotion", label: t("billing.paymentLinks.promotionCode"), value: l.promotion_code, mono: true },
    { key: "customer", label: t("billing.paymentLinks.customer"), value: l.customer_external_id },
    { key: "uses", label: t("billing.paymentLinks.uses"), value: usesOf(l) },
    { key: "expires", label: t("billing.paymentLinks.expires"), value: when(l.expire_time) },
    { key: "created", label: t("billing.paymentLinks.createdAt"), value: when(l.create_time) },
    { key: "id", label: t("billing.paymentLinks.id"), value: l.id, mono: true },
  ];
});
const what = (l: { kind?: string; amount_minor?: string; currency?: string; invoice_id?: string }) =>
  l.kind !== "invoice" ? formatMinor(l.amount_minor, l.currency, locale.value) : (l.invoice_id ?? "");
</script>

<template>
  <Card class="p-3">
    <DataToolbar v-model:search="query" :active="active" @clear="clear">
      <FilterSelect v-model="kind" :label="t('billing.paymentLinks.kind')" :options="kinds" />
      <FilterSelect v-model="status" :label="t('billing.paymentLinks.status')" :options="statuses" />
      <DateRangePicker v-model="range" />
      <template #actions>
        <Button type="button" @click="open = true"><Plus class="size-4" aria-hidden="true" />{{ t("billing.paymentLinks.create") }}</Button>
      </template>
    </DataToolbar>
    <ErrorText :error="links.error.value" class="px-3" />
    <ErrorText :error="act.revoke.error.value" class="px-3" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.paymentLinks.id") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.kind") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.for") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.customer") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.status") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.paymentLinks.uses") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.expires") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.createdAt") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(l, i) in shown" :key="l.id" class="cursor-pointer" @click="detail = i">
          <TableCell class="mono text-xs font-medium">{{ l.id?.slice(0, 8) }}</TableCell>
          <TableCell>{{ t(`billing.paymentLinks.kinds.${l.kind ?? 'adhoc'}`) }}<span v-if="l.single_use" class="text-muted-foreground block text-xs">{{ t("billing.paymentLinks.singleUseShort") }}</span></TableCell>
          <TableCell>
            <span :class="l.kind === 'invoice' ? 'mono text-xs break-all' : 'tabular-nums'">{{ what(l) }}</span>
            <span v-if="l.description" class="text-muted-foreground block max-w-64 truncate text-xs">{{ l.description }}</span>
          </TableCell>
          <TableCell>{{ l.customer_external_id || "—" }}</TableCell>
          <TableCell><StatusPill :tone="toneOf(l.status ?? 'active')">{{ t(`billing.paymentLinks.statuses.${l.status ?? 'active'}`) }}</StatusPill></TableCell>
          <TableCell class="text-end text-xs tabular-nums">{{ usesOf(l) }}</TableCell>
          <TableCell class="text-xs whitespace-nowrap">{{ when(l.expire_time) }}</TableCell>
          <TableCell class="text-xs whitespace-nowrap">{{ when(l.create_time) }}</TableCell>
          <TableCell><RowActions :actions="rowActions(l, i)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!links.isPending.value && list.length === 0 && !links.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.paymentLinks.empty") }}</p>
    <p v-else-if="shown.length === 0 && active" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination :page="history.length + 1" :has-prev="history.length > 0" :has-next="!!links.data.value?.next_page_token" @prev="back" @next="next" />

    <DetailSheet v-model:index="detail" :total="shown.length" :title="t('billing.paymentLinks.title')" :description="current?.id ?? ''" :edge="nav.edge.value" @edge="nav.onEdge">
      <DetailList :rows="detailRows">
        <template #status><StatusPill :tone="toneOf(current?.status ?? 'active')">{{ t(`billing.paymentLinks.statuses.${current?.status ?? 'active'}`) }}</StatusPill></template>
      </DetailList>
      <PaymentContext v-if="current && (current.invoice_id || current.customer_external_id)" :key="current.id" :payer="false" :tenant-id="tenantId" :invoice-id="current.invoice_id" :customer="current.customer_external_id" class="mt-6" />
      <template v-if="canRevoke(current)" #footer>
        <Button type="button" variant="destructive" :disabled="act.revoke.isPending.value" @click="revoke(current?.id)">{{ t("billing.paymentLinks.revoke") }}</Button>
      </template>
    </DetailSheet>

    <Sheet v-model:open="open" name="new-payment-link">
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{{ t("billing.paymentLinks.title") }}</SheetTitle>
          <SheetDescription>{{ t("billing.paymentLinks.hint") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form id="payment-link-form" class="grid gap-4" role="form" :aria-label="t('billing.paymentLinks.formLabel')" @submit.prevent="submit">
            <AppField />
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
