<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { formatMinor, parseMajor } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreateCreditNote, useCreditNotes } from "./api";

/** Credit notes of a tenant, or of one invoice; with `issuable` a paid invoice can be credited here. */
const props = defineProps<{ tenantId: string; invoiceId?: string; currency?: string; issuable?: boolean }>();
const { t, locale } = useI18n();
const notes = useCreditNotes(
  () => props.tenantId,
  () => props.invoiceId ?? "",
);
const create = useCreateCreditNote(() => props.tenantId);
const amount = ref("");
const reason = ref("");
const formError = ref("");
const date = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium" }).format(new Date(v)) : "—");
const list = computed(() => notes.data.value?.credit_notes ?? []);

function issue() {
  formError.value = "";
  let minor = "0";
  if (amount.value.trim() !== "") {
    const parsed = parseMajor(amount.value, props.currency ?? "USD");
    if (parsed === null || parsed === "0") {
      formError.value = t("billing.creditNotes.badAmount", { currency: props.currency ?? "" });
      return;
    }
    minor = parsed;
  }
  create.mutate({ invoiceId: props.invoiceId ?? "", amountMinor: minor, reason: reason.value.trim() }, { onSuccess: () => (amount.value = reason.value = "") });
}
</script>

<template>
  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.creditNotes.title") }}</h2>
    <form v-if="issuable" class="grid gap-3 px-3 sm:grid-cols-[1fr_2fr_auto]" role="form" :aria-label="t('billing.creditNotes.formLabel')" @submit.prevent="issue">
      <Input v-model="amount" inputmode="decimal" :aria-label="t('billing.creditNotes.amount')" :placeholder="t('billing.creditNotes.amountPlaceholder')" />
      <Input v-model="reason" maxlength="500" :aria-label="t('billing.creditNotes.reason')" :placeholder="t('billing.creditNotes.reason')" />
      <Button type="submit" :disabled="create.isPending.value">{{ t("billing.creditNotes.create") }}</Button>
    </form>
    <p v-if="formError" class="text-destructive px-3 text-sm" role="alert">{{ formError }}</p>
    <p v-if="create.error.value" class="text-destructive px-3 text-sm" role="alert">{{ toApiError(create.error.value).message }}</p>
    <p v-if="notes.error.value" class="text-destructive px-3 text-sm" role="alert">{{ toApiError(notes.error.value).message }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.creditNotes.number") }}</TableHead>
          <TableHead v-if="!invoiceId">{{ t("billing.creditNotes.invoice") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.customer") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.reason") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.date") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.creditNotes.amount") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="n in list" :key="n.id">
          <TableCell class="mono font-medium">{{ n.number }}</TableCell>
          <TableCell v-if="!invoiceId" class="mono">{{ n.invoice_number }}</TableCell>
          <TableCell>{{ n.customer_external_id }}</TableCell>
          <TableCell>{{ n.reason }}</TableCell>
          <TableCell>{{ date(n.create_time) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(n.total_minor, n.currency, locale) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!notes.isPending.value && list.length === 0 && !notes.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.creditNotes.empty") }}</p>
  </Card>
</template>
