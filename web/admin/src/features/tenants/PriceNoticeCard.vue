<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { usePriceChangeNotice, useSetPriceChangeNotice } from "./api";
import { useCanWrite } from "@/shared/lib/access";

const canWrite = useCanWrite();

/**
 * The operator's notice rules for price changes (plan 15): the notice a seller's run uses when it names none, and the least notice per country (`*` is any country
 * without a row of its own). The rows are the operator's words and say so: unverified, not legal advice. A country row can raise the code's floor of 14 days, never
 * lower it; the next run reads the table, a price change that has scheduled a subscription keeps what it told the customer.
 */
const { t } = useI18n();
const fmt = useFormat();
const notice = usePriceChangeNotice();
const set = useSetPriceChangeNotice();
type Row = { country: string; min_notice_days: string; note: string };
const defaultDays = ref("30");
const rows = ref<Row[]>([]);

watch(
  () => notice.data.value,
  (n) => {
    if (!n) return;
    defaultDays.value = String(n.default_notice_days);
    rows.value = n.rules.map((r) => ({ country: r.country, min_notice_days: String(r.min_notice_days), note: r.note }));
  },
  { immediate: true },
);

const addRow = () => rows.value.push({ country: "", min_notice_days: "14", note: "" });
const removeRow = (i: number) => rows.value.splice(i, 1);
function save() {
  set.mutate({
    default_notice_days: Number(defaultDays.value),
    rules: rows.value.map((r) => ({ country: r.country.trim().toUpperCase(), min_notice_days: Number(r.min_notice_days), note: r.note.trim() })),
  });
}
</script>

<template>
  <Card class="mb-4 flex flex-col gap-3 p-5" data-testid="price-notice">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="m-0 text-base font-semibold">{{ t("tenants.priceNotice.title") }}</h2>
      <StatusPill tone="warning">{{ t("tenants.priceNotice.unverified") }}</StatusPill>
    </div>
    <p class="text-muted-foreground m-0 text-sm">{{ t("tenants.priceNotice.hint") }}</p>
    <Skeleton v-if="notice.isPending.value" class="h-24 w-full" />
    <ErrorState :error="notice.error.value" :retrying="notice.isFetching.value" compact @retry="notice.refetch()" />
    <ErrorText :error="set.error.value" />
    <form v-if="notice.data.value" class="flex flex-col gap-3" @submit.prevent="save">
      <FormField :label="t('tenants.priceNotice.defaultDays')" for="notice-default" :hint="t('tenants.priceNotice.defaultHint')">
        <Input id="notice-default" v-model="defaultDays" type="number" min="14" max="365" required class="max-w-40" data-testid="price-notice-default" />
      </FormField>
      <div class="flex flex-col gap-2" role="group" :aria-label="t('tenants.priceNotice.rules')">
        <p class="m-0 text-sm font-medium">{{ t("tenants.priceNotice.rules") }}</p>
        <div v-for="(r, i) in rows" :key="i" class="grid items-end gap-2 sm:grid-cols-[6rem_8rem_1fr_auto]" :data-testid="`price-notice-row-${i}`">
          <label class="grid gap-1 text-xs">{{ t("tenants.priceNotice.country") }}
            <Input v-model="r.country" maxlength="2" required placeholder="TR" :data-testid="`price-notice-country-${i}`" />
          </label>
          <label class="grid gap-1 text-xs">{{ t("tenants.priceNotice.days") }}
            <Input v-model="r.min_notice_days" type="number" min="14" max="365" required :data-testid="`price-notice-days-${i}`" />
          </label>
          <label class="grid gap-1 text-xs">{{ t("tenants.priceNotice.note") }}
            <Input v-model="r.note" maxlength="200" dir="auto" :data-testid="`price-notice-note-${i}`" />
          </label>
          <Button type="button" variant="ghost" size="sm" :data-testid="`price-notice-remove-${i}`" @click="removeRow(i)">{{ t("tenants.priceNotice.remove") }}</Button>
        </div>
        <p class="text-muted-foreground m-0 text-xs">{{ t("tenants.priceNotice.star") }}</p>
      </div>
      <p v-if="notice.data.value.update_time" class="text-muted-foreground m-0 text-xs">
        {{ t("tenants.priceNotice.updated", { who: notice.data.value.updated_by, date: fmt.date(notice.data.value.update_time, "dateTime") }) }}
      </p>
      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" :disabled="!canWrite" data-testid="price-notice-add" @click="addRow">{{ t("tenants.priceNotice.add") }}</Button>
        <Button type="submit" :disabled="!canWrite || set.isPending.value" data-testid="price-notice-save">{{ t("tenants.priceNotice.save") }}</Button>
      </div>
      <p v-if="set.isSuccess.value" class="text-success m-0 text-sm" role="status" data-testid="price-notice-saved">{{ t("tenants.priceNotice.saved") }}</p>
    </form>
  </Card>
</template>
