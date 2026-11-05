<script setup lang="ts">
import { Loader2, RefreshCw } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorState } from "@/shared/ui/error-state";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { Sparkline } from "@/shared/ui/sparkline";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { EVENT_ERROR_CODES, eventErrorHintKey } from "./eventErrors";
import { useRejectedEvents, useRejectedSummary } from "./rejectedApi";

/**
 * The events the API refused, with what to do about each (plan 18): the error code and its hint in the reader's language, the meter and customer as the developer sent
 * them, the event's time and when it arrived. Newest first, a page at a time, filtered by code and meter; above it the last 24 hours by code (a code is a filter) and by hour.
 * It only reads, so a viewer sees it too. The properties of an event are not kept, so there is nothing of them to show.
 */
const props = defineProps<{ tenantId: string }>();
const { t, te } = useI18n();
const fmt = useFormat();

const code = ref("");
// What is typed; it becomes the filter when the box is left or Enter is pressed (a request per keystroke would be a lot to ask of a list).
const meterDraft = ref("");
const meter = ref("");
watch(() => props.tenantId, () => (code.value = meter.value = meterDraft.value = ""));
const list = useRejectedEvents(() => props.tenantId, code, meter);
const summary = useRejectedSummary(() => props.tenantId);
const rows = computed(() => (list.data.value?.pages ?? []).flatMap((p) => p.events ?? []));
const filtering = computed(() => code.value !== "" || meter.value !== "");
const codeOptions = computed(() => EVENT_ERROR_CODES.map((c) => ({ value: c, label: c })));
const hourly = computed(() => (summary.data.value?.hourly ?? []).map((h) => h.rejected));
const total = computed(() => summary.data.value?.total ?? 0);
const applyMeter = () => (meter.value = meterDraft.value.trim());
function clear() {
  code.value = meter.value = meterDraft.value = "";
}
function refresh() {
  void list.refetch();
  void summary.refetch();
}
const hint = (c: string | undefined) => {
  const key = eventErrorHintKey(c);
  return key && te(key) ? t(key) : "";
};
const none = "—";
</script>

<template>
  <Card class="mb-4 p-5" data-testid="rejected-events">
    <div class="mb-1 flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-base font-semibold">{{ t("connect.rejected.title") }}</h2>
      <Button type="button" variant="ghost" size="sm" :disabled="list.isFetching.value" data-testid="rejected-refresh" @click="refresh">
        <RefreshCw class="size-4" :class="{ 'animate-spin': list.isFetching.value }" aria-hidden="true" />{{ t("connect.rejected.refresh") }}
      </Button>
    </div>
    <p class="text-muted-foreground mb-3 text-sm">{{ t("connect.rejected.help") }}</p>

    <div v-if="summary.data.value && total > 0" class="bg-muted/40 mb-4 flex flex-wrap items-center gap-x-6 gap-y-3 rounded-2xl p-4" data-testid="rejected-summary">
      <div>
        <p class="text-sm font-medium" data-testid="rejected-total">{{ t("connect.rejected.last24h", { count: fmt.count(total) }) }}</p>
        <Sparkline :values="hourly" :width="160" :height="36" class="mt-1" />
      </div>
      <ul class="flex min-w-0 flex-1 flex-wrap gap-2" :aria-label="t('connect.rejected.byCode')">
        <li v-for="c in summary.data.value.by_code" :key="c.error_code">
          <Button type="button" variant="outline" size="sm" :aria-pressed="code === c.error_code" :data-testid="`rejected-chip-${c.error_code}`" @click="code = code === c.error_code ? '' : c.error_code">
            <span class="mono" dir="ltr">{{ c.error_code }}</span> <span class="text-muted-foreground tabular-nums">{{ fmt.count(c.count) }}</span>
          </Button>
        </li>
      </ul>
    </div>

    <div class="mb-3 grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
      <label class="grid gap-1 text-sm">{{ t("connect.rejected.filters.code") }}
        <NativeSelect v-model="code" data-testid="rejected-code">
          <option value="">{{ t("connect.rejected.filters.anyCode") }}</option>
          <option v-for="o in codeOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
        </NativeSelect>
      </label>
      <label class="grid gap-1 text-sm">{{ t("connect.rejected.filters.meter") }}
        <Input v-model="meterDraft" spellcheck="false" :placeholder="t('connect.rejected.filters.anyMeter')" data-testid="rejected-meter" @change="applyMeter" @keydown.enter="applyMeter" />
      </label>
      <Button v-if="filtering || meterDraft !== ''" type="button" variant="ghost" data-testid="rejected-clear" @click="clear">{{ t("connect.rejected.clear") }}</Button>
    </div>

    <ErrorState :error="list.error.value" :retrying="list.isFetching.value" compact @retry="refresh" />
    <Skeleton v-if="list.isPending.value && !list.error.value" class="h-32 rounded-2xl" data-testid="rejected-loading" />
    <template v-else-if="!list.error.value || rows.length > 0">
      <Table v-if="rows.length > 0">
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("connect.rejected.columns.code") }}</TableHead>
            <TableHead>{{ t("connect.rejected.columns.meter") }}</TableHead>
            <TableHead>{{ t("connect.rejected.columns.customer") }}</TableHead>
            <TableHead>{{ t("connect.rejected.columns.eventTime") }}</TableHead>
            <TableHead>{{ t("connect.rejected.columns.received") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="e in rows" :key="e.id" :data-testid="`rejected-row-${e.id}`">
            <TableCell class="max-w-md">
              <span class="mono text-xs font-medium break-all" dir="ltr">{{ e.error_code }}</span>
              <span v-if="hint(e.error_code)" class="text-muted-foreground mt-1 block text-xs" data-testid="rejected-hint">{{ hint(e.error_code) }}</span>
              <span class="text-muted-foreground mt-1 block text-xs break-words" dir="ltr" data-testid="rejected-message">{{ e.error_message }}</span>
              <span v-if="e.idempotency_key" class="mono text-muted-foreground mt-1 block text-xs break-all" dir="ltr">{{ e.idempotency_key }}</span>
            </TableCell>
            <TableCell class="mono text-xs break-all" dir="ltr">{{ e.meter_code || none }}</TableCell>
            <TableCell class="mono text-xs break-all" dir="ltr">{{ e.customer_external_id || none }}</TableCell>
            <TableCell class="text-xs whitespace-nowrap tabular-nums">{{ fmt.date(e.event_time, "dateTime", none) }}</TableCell>
            <TableCell class="text-xs whitespace-nowrap tabular-nums">{{ fmt.date(e.received_time, "dateTime", none) }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <TableEmpty v-else data-testid="rejected-empty">{{ t(filtering ? 'common.noMatches' : 'connect.rejected.empty') }}</TableEmpty>
      <div v-if="list.hasNextPage.value" class="flex justify-center pt-3">
        <Button type="button" variant="outline" :disabled="list.isFetchingNextPage.value" data-testid="rejected-more" @click="list.fetchNextPage()">
          <Loader2 v-if="list.isFetchingNextPage.value" class="animate-spin" aria-hidden="true" />{{ t("connect.rejected.loadMore") }}
        </Button>
      </div>
    </template>
  </Card>
</template>
