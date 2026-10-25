<script setup lang="ts">
import { Check, TriangleAlert } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { useErrorText } from "@/shared/lib/errors";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { downloadJournalCsv, useJournal, type JournalFilter } from "./api";
import { dayEnd, dayStart, journalTotals } from "./logic";
import Pager from "./Pager.vue";
import { usePager } from "./paging";

const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const errorText = useErrorText();
const tenantId = computed(() => props.tenantId);

const fromDay = ref("");
const toDay = ref("");
const filter = computed<JournalFilter>(() => ({ currency: "", from: dayStart(fromDay.value) ?? "", to: dayEnd(toDay.value) ?? "" }));
const pager = usePager();
watch(filter, () => pager.reset());
const journal = useJournal(tenantId, filter, pager.token);
const totals = computed(() => journalTotals(journal.data.value?.totals));

const exporting = ref(false);
async function exportCsv() {
  exporting.value = true;
  try {
    await downloadJournalCsv(props.tenantId, filter.value);
  } catch (e) {
    toast.error(errorText(e));
  } finally {
    exporting.value = false;
  }
}
</script>

<template>
  <div class="flex flex-wrap items-end justify-between gap-3">
    <div>
      <h2 class="text-lg font-semibold">{{ t("affiliates.journal.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("affiliates.journal.intro") }}</p>
    </div>
    <Button type="button" variant="outline" :disabled="exporting" @click="exportCsv">{{ t("affiliates.journal.download") }}</Button>
  </div>

  <div class="flex flex-wrap items-center gap-2">
    <Input v-model="fromDay" type="date" class="w-40" :aria-label="t('billing.accounting.period.from')" />
    <Input v-model="toDay" type="date" class="w-40" :aria-label="t('billing.accounting.period.to')" />
  </div>

  <ErrorText :error="journal.error.value" />
  <Skeleton v-if="journal.isPending.value" class="h-32" />
  <template v-else-if="journal.data.value">
    <section v-for="c in totals" :key="c.currency" :aria-label="c.currency" :data-balanced="c.balanced">
      <Card>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="text-lg font-semibold">{{ c.currency }}</h3>
          <StatusPill :tone="c.balanced ? 'success' : 'danger'">
            <Check v-if="c.balanced" class="size-3.5" aria-hidden="true" /><TriangleAlert v-else class="size-3.5" aria-hidden="true" />
            {{ c.balanced ? t("affiliates.journal.balanced") : t("affiliates.journal.unbalanced") }}
          </StatusPill>
        </div>
        <dl class="m-0 grid gap-4 sm:grid-cols-2">
          <div data-stat="debit">
            <dt class="text-muted-foreground text-sm">{{ t("affiliates.journal.debits") }}</dt>
            <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.debit, c.currency) }}</dd>
          </div>
          <div data-stat="credit">
            <dt class="text-muted-foreground text-sm">{{ t("affiliates.journal.credits") }}</dt>
            <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.credit, c.currency) }}</dd>
          </div>
        </dl>
      </Card>
    </section>

    <Card class="p-3">
      <p v-if="!journal.data.value.items.length" class="text-muted-foreground px-3 py-4 text-sm">{{ t("affiliates.journal.empty") }}</p>
      <Table v-else>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("affiliates.journal.time") }}</TableHead>
            <TableHead>{{ t("affiliates.journal.account") }}</TableHead>
            <TableHead class="text-end">{{ t("affiliates.journal.debit") }}</TableHead>
            <TableHead class="text-end">{{ t("affiliates.journal.credit") }}</TableHead>
            <TableHead>{{ t("affiliates.journal.memo") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="e in journal.data.value.items" :key="e.id" :data-account="e.account">
            <TableCell class="whitespace-nowrap">{{ fmt.date(e.post_time, "short") }}</TableCell>
            <TableCell>{{ t(`affiliates.journal.accounts.${e.account}`) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ e.debit_minor && e.debit_minor !== "0" ? fmt.amount(e.debit_minor, e.currency) : "" }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ e.credit_minor && e.credit_minor !== "0" ? fmt.amount(e.credit_minor, e.currency) : "" }}</TableCell>
            <TableCell dir="auto" class="min-w-0">
              {{ e.memo }}
              <StatusPill v-if="e.fake" tone="warning" class="ms-2" data-testid="fake-badge">{{ t("affiliates.payouts.fake") }}</StatusPill>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <Pager :has-prev="pager.hasPrev.value" :next="journal.data.value.next" :total="journal.data.value.total" @prev="pager.prev()" @next="pager.next(journal.data.value.next)" />
    </Card>
  </template>
</template>
