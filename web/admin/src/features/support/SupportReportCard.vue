<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { duration } from "./duration";
import { PRIORITIES, STATUSES, useSupportReport } from "./api";

/**
 * The operator's report of the queue: what waits for us and how much of it is late, how the first answers of the last 30 days went (how many came in time, how long
 * they took, median and average), and every ticket by status and the waiting ones by priority. The same numbers the sidebar's badge and the alert read.
 */
const { t, locale } = useI18n();
const report = useSupportReport();
const r = computed(() => report.data.value);
const onTime = computed(() => (r.value && r.value.answered > 0 ? Math.round((r.value.answered_on_time / r.value.answered) * 100) : null));
const counted = (list: { value: string; count: number }[] | undefined, key: string) => list?.find((c) => c.value === key)?.count ?? 0;
</script>

<template>
  <Card class="mb-4 flex flex-col gap-4 p-5" data-testid="support-report">
    <h2 class="m-0 text-base font-semibold">{{ t("support.report.title") }}</h2>
    <Skeleton v-if="report.isPending.value" class="h-16 w-full" aria-busy="true" />
    <ErrorText :error="report.error.value" />
    <template v-if="r">
      <dl class="m-0 grid grid-cols-2 gap-4 md:grid-cols-4">
        <div><dt class="text-muted-foreground text-xs">{{ t("support.report.waiting") }}</dt><dd class="m-0 text-2xl font-semibold tabular-nums" data-testid="report-waiting">{{ r.waiting }}</dd></div>
        <div><dt class="text-muted-foreground text-xs">{{ t("support.report.late") }}</dt><dd class="m-0 text-2xl font-semibold tabular-nums" :class="r.breached > 0 ? 'text-destructive' : ''" data-testid="report-late">{{ r.breached }}</dd></div>
        <div>
          <dt class="text-muted-foreground text-xs">{{ t("support.report.median", { days: r.window_days }) }}</dt>
          <dd class="m-0 text-2xl font-semibold tabular-nums" data-testid="report-median">{{ r.answered > 0 ? duration(r.median_first_answer_seconds, locale) : "—" }}</dd>
          <dd v-if="r.answered > 0" class="text-muted-foreground m-0 text-xs" data-testid="report-average">{{ t("support.report.average", { time: duration(r.average_first_answer_seconds, locale) }) }}</dd>
        </div>
        <div>
          <dt class="text-muted-foreground text-xs">{{ t("support.report.onTime") }}</dt>
          <dd class="m-0 text-2xl font-semibold tabular-nums" data-testid="report-on-time">{{ onTime === null ? "—" : `${onTime}%` }}</dd>
          <dd class="text-muted-foreground m-0 text-xs">{{ t("support.report.answeredOf", { answered: r.answered, opened: r.opened }) }}</dd>
        </div>
      </dl>
      <div class="grid gap-4 md:grid-cols-2">
        <div>
          <h3 class="m-0 mb-1 text-xs font-medium">{{ t("support.report.byStatus") }}</h3>
          <ul class="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-sm" data-testid="report-by-status">
            <li v-for="s in STATUSES" :key="s"><span class="text-muted-foreground">{{ t(`support.status.${s}`) }}</span> <span class="font-medium tabular-nums">{{ counted(r.by_status, s) }}</span></li>
          </ul>
        </div>
        <div>
          <h3 class="m-0 mb-1 text-xs font-medium">{{ t("support.report.byPriority") }}</h3>
          <ul class="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-sm" data-testid="report-by-priority">
            <li v-for="p in PRIORITIES" :key="p"><span class="text-muted-foreground">{{ t(`support.priority.${p}`) }}</span> <span class="font-medium tabular-nums">{{ counted(r.by_priority, p) }}</span></li>
          </ul>
        </div>
      </div>
    </template>
  </Card>
</template>
