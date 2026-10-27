<script setup lang="ts">
import { ArrowUpRight, Ban, Plus } from "@lucide/vue";
import { useParams, useSearch } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { matchesSearch } from "@/shared/lib/paging";
import { PaymentContext } from "@/features/related";
import { useEdgeDetail } from "@/shared/lib/useEdgeDetail";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { usePaymentLinkActions, usePaymentLinks } from "./api";
import PaymentLinkSheet from "./PaymentLinkSheet.vue";

const { t, locale } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));

// A page can be opened from an invoice or a customer with the form prefilled (?invoice=…, ?customer=…); the sheet follows the address when it changes in the page.
const search = useSearch({ strict: false });
const prefill = computed(() => ({ invoice: String(search.value.invoice ?? ""), customer: String(search.value.customer ?? "") }));
// The form opens on its own when the page was reached from an invoice or a customer.
const open = ref(prefill.value.invoice !== "" || prefill.value.customer !== "");

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

    <PaymentLinkSheet v-model:open="open" :tenant-id="tenantId" :invoice="prefill.invoice" :customer="prefill.customer" />
  </Card>
</template>
