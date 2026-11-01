<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { usePriceChange, usePriceChanges, useRetryPriceChange, useWithdrawPriceChange, type PriceChangeItem } from "./api";

/**
 * The price changes made for one package: who made each, from when it takes effect, how many subscriptions are waiting, moved, withdrawn or skipped, and for
 * each subscription what became of it and when its customer was mailed. A skipped subscription can be tried again (an open price change), and everything that is
 * still waiting can be withdrawn in one go. Applied changes stay; mails already sent are not taken back.
 */
const props = defineProps<{ tenantId: string; code: string; open: boolean }>();
const { t } = useI18n();
const fmt = useFormat();
const list = usePriceChanges(() => props.tenantId, () => props.code, () => props.open);
const retry = useRetryPriceChange(() => props.tenantId);
const withdraw = useWithdrawPriceChange(() => props.tenantId);
const opened = ref("");
const detail = usePriceChange(() => props.tenantId, opened);
const notice = ref("");

const tone = (state: PriceChangeItem["state"]) => ({ scheduled: "info", applied: "success", withdrawn: "neutral", skipped: "warning" })[state] as "info" | "success" | "neutral" | "warning";

async function tryAgain(id: string) {
  notice.value = "";
  const r = await retry.mutateAsync(id).catch(() => null);
  if (r) notice.value = t("billing.packages.priceChange.retried", { n: r.scheduled, skipped: r.skipped.length });
}
async function withdrawAll(id: string, n: number) {
  notice.value = "";
  if (!(await askConfirm({ body: t("billing.packages.priceChange.confirmWithdraw", { n }), confirmLabel: t("billing.packages.priceChange.withdraw") }))) return;
  const r = await withdraw.mutateAsync(id).catch(() => null);
  if (r) notice.value = t("billing.packages.priceChange.withdrawn", { n: r.withdrawn });
}
</script>

<template>
  <section v-if="(list.data.value ?? []).length > 0" class="grid gap-2" data-testid="pc-history">
    <h3 class="text-sm font-medium">{{ t("billing.packages.priceChange.history") }}</h3>
    <ErrorText :error="retry.error.value ?? withdraw.error.value" />
    <p v-if="notice" class="bg-success/10 text-success rounded-2xl px-4 py-2 text-sm" role="status" data-testid="pc-notice-done">{{ notice }}</p>
    <div v-for="pc in list.data.value" :key="pc.id" class="border-border rounded-2xl border p-3 text-sm" :data-testid="`pc-row-${pc.id}`">
      <div class="flex flex-wrap items-center gap-2">
        <span class="font-medium">{{ t("billing.packages.priceChange.versions", { from: pc.from_version, to: pc.to_version }) }}</span>
        <StatusPill :tone="pc.state === 'open' ? 'info' : 'neutral'">{{ t(`billing.packages.priceChange.state.${pc.state}`) }}</StatusPill>
        <span class="text-muted-foreground text-xs">{{ t("billing.packages.priceChange.madeBy", { date: fmt.date(pc.create_time, "day", "—"), who: pc.created_by || "—" }) }}</span>
      </div>
      <p class="text-muted-foreground mt-1 text-xs">
        {{ t("billing.packages.priceChange.takesEffect", { date: fmt.date(pc.effective_time, "day", "—"), n: pc.notice_days }) }}
        · {{ t("billing.packages.priceChange.counts", { scheduled: pc.scheduled, applied: pc.applied, withdrawn: pc.withdrawn, skipped: pc.skipped }) }}
      </p>
      <div class="mt-2 flex flex-wrap gap-2">
        <Button type="button" size="sm" variant="ghost" :data-testid="`pc-details-${pc.id}`" @click="opened = opened === pc.id ? '' : pc.id">
          {{ opened === pc.id ? t("billing.packages.priceChange.hideDetails") : t("billing.packages.priceChange.details") }}
        </Button>
        <Button v-if="pc.state === 'open' && pc.skipped > 0" type="button" size="sm" variant="outline" :disabled="retry.isPending.value" :data-testid="`pc-retry-${pc.id}`" @click="tryAgain(pc.id)">
          {{ t("billing.packages.priceChange.retry", { n: pc.skipped }) }}
        </Button>
        <Button v-if="pc.state === 'open'" type="button" size="sm" variant="outline" :disabled="withdraw.isPending.value" :data-testid="`pc-withdraw-${pc.id}`" @click="withdrawAll(pc.id, pc.scheduled)">
          {{ t("billing.packages.priceChange.withdraw") }}
        </Button>
      </div>
      <Table v-if="opened === pc.id && detail.data.value" class="mt-2" :data-testid="`pc-items-${pc.id}`">
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.packages.priceChange.columns.customer") }}</TableHead>
            <TableHead>{{ t("billing.packages.priceChange.columns.state") }}</TableHead>
            <TableHead>{{ t("billing.packages.priceChange.columns.effective") }}</TableHead>
            <TableHead>{{ t("billing.packages.priceChange.columns.notified") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="it in detail.data.value.items" :key="it.subscription_id">
            <TableCell class="mono">{{ it.customer_external_id }}</TableCell>
            <TableCell>
              <StatusPill :tone="tone(it.state)">{{ t(`billing.packages.priceChange.itemState.${it.state}`) }}</StatusPill>
              <span v-if="it.reason" class="text-muted-foreground ms-2 text-xs">{{ t(`billing.packages.priceChange.skipWhy.${it.reason}`) }}</span>
            </TableCell>
            <TableCell class="whitespace-nowrap">{{ fmt.date(it.effective_time, "day", "—") }}</TableCell>
            <TableCell class="whitespace-nowrap">{{ fmt.date(it.notify_time, "dateTime", "—") }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
  </section>
</template>
