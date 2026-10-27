<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { validLinkDays } from "@/shared/lib/rules";
import { useTableState } from "@/shared/lib/useTableState";
import { absoluteUrl } from "@/shared/lib/url";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { Input } from "@/shared/ui/input";
import { FilterSelect } from "@/shared/ui/filter-select";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCustomerPayments, usePortalLinkActions } from "./api";

const props = defineProps<{ tenantId: string; customer: string }>();
const emit = defineEmits<{ close: [] }>();
const { t, locale } = useI18n();
const fmt = useFormat();

const days = ref("30");
const daysError = ref("");
const act = usePortalLinkActions(() => props.tenantId);
const payments = useCustomerPayments(
  () => props.tenantId,
  () => props.customer,
);
const rows = computed(() => payments.data.value?.payments ?? []);
type Payment = (typeof rows.value)[number];
const table = useTableState<Payment>(rows, {
  searchFields: (p) => [p.invoice_number, p.invoice_id, p.provider],
  filters: { status: (p, v) => p.status === v },
});
const statusFilter = computed({ get: () => table.filters.value.status ?? [], set: (v: string[]) => (table.filters.value.status = v) });
const statusOptions = computed(() => [{ value: "", label: t("billing.paymentLinks.anyStatus") }, ...["succeeded", "pending", "failed"].map((s) => ({ value: s, label: s }))]);
const url = computed(() => absoluteUrl((act.create.data.value as { url?: string } | undefined)?.url, window.location.origin));

function make() {
  daysError.value = "";
  const n = Number(days.value);
  if (!validLinkDays(n)) {
    daysError.value = t("billing.paymentLinks.errors.days");
    return;
  }
  act.create.mutate({ externalId: props.customer, days: n });
}
function revoke() {
  if (window.confirm(t("billing.paymentLinks.portal.confirmRevoke"))) act.revoke.mutate(props.customer);
}
const when = (v?: string) => fmt.date(v, "short", "—");
</script>

<template>
  <Card class="mb-6" role="region" :aria-label="t('billing.paymentLinks.portal.title', { id: customer })">
    <div class="flex items-center justify-between">
      <h2 class="text-lg font-semibold">{{ t("billing.paymentLinks.portal.title", { id: customer }) }}</h2>
      <Button size="sm" variant="ghost" @click="emit('close')">{{ t("billing.paymentLinks.portal.close") }}</Button>
    </div>
    <p class="text-muted-foreground text-sm">{{ t("billing.paymentLinks.portal.hint") }}</p>
    <form class="flex flex-wrap items-center gap-3" @submit.prevent="make">
      <Input v-model="days" inputmode="numeric" class="w-28" :aria-label="t('billing.paymentLinks.days')" />
      <Button type="submit" :disabled="act.create.isPending.value">{{ t("billing.paymentLinks.portal.create") }}</Button>
      <Button type="button" variant="outline" :disabled="act.revoke.isPending.value" @click="revoke">{{ t("billing.paymentLinks.portal.revoke") }}</Button>
    </form>
    <CopyField v-if="url" :value="url" :label="t('billing.paymentLinks.portal.link')" :copy-label="t('billing.paymentLinks.copy')" testid="portal-link-url"><span class="text-muted-foreground text-xs">{{ t("billing.paymentLinks.shownOnce") }}</span></CopyField>
    <p v-if="act.revoke.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.paymentLinks.portal.revoked") }}</p>
    <ErrorText :message="daysError" />
    <ErrorText :error="act.create.error.value" />
    <ErrorText :error="act.revoke.error.value" />

    <h3 class="mt-2 font-medium">{{ t("billing.paymentLinks.portal.payments") }}</h3>
    <ErrorText :error="payments.error.value" />
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" class="px-0" @clear="table.clear()">
      <FilterSelect v-model="statusFilter" multiple :label="t('billing.paymentLinks.status')" :options="statusOptions" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.paymentLinks.portal.date") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.portal.invoice") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.portal.type") }}</TableHead>
          <TableHead>{{ t("billing.paymentLinks.status") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.paymentLinks.amount") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(p, i) in table.items.value" :key="i">
          <TableCell class="text-xs">{{ when(p.occurred_at) }}</TableCell>
          <TableCell class="mono text-xs">{{ p.invoice_number || p.invoice_id }}</TableCell>
          <TableCell>{{ t(`billing.paymentLinks.portal.kinds.${p.kind ?? 'charge'}`) }} <span class="text-muted-foreground text-xs">{{ p.provider }}</span></TableCell>
          <TableCell><StatusPill :tone="toneOf(p.status)">{{ p.status }}</StatusPill></TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(p.amount_minor, p.currency, locale) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!payments.isPending.value && rows.length === 0 && !payments.error.value" class="text-muted-foreground text-center text-sm">{{ t("billing.paymentLinks.portal.noPayments") }}</p>
    <p v-if="rows.length > 0 && table.total.value === 0" class="text-muted-foreground text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>
</template>
