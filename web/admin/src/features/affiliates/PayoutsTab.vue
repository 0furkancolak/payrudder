<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useAffiliateOptions, useCancelPayout, useCreatePayouts, usePayouts } from "./api";
import CommissionsTable from "./CommissionsTable.vue";
import { payoutTone, type Payout } from "./logic";
import MarkPaidDialog from "./MarkPaidDialog.vue";
import { DataPagination } from "@/shared/ui/data-table";
import { usePager } from "./paging";

const props = defineProps<{ tenantId: string; focus?: string }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
const tenantId = computed(() => props.tenantId);

const status = ref("");
const pager = usePager();
watch(status, () => pager.reset());
const list = usePayouts(tenantId, status, pager.token, pager.size);
const options = useAffiliateOptions(tenantId);
const codeOf = (id: string | undefined) => (options.data.value ?? []).find((a) => a.id === id)?.code ?? (id ?? "").slice(0, 8);

const create = useCreatePayouts(tenantId);
const cancel = useCancelPayout(tenantId);
const result = computed(() => create.data.value);

const paying = ref<Payout | null>(null);
const payOpen = ref(false);
function pay(p: Payout) {
  paying.value = p;
  payOpen.value = true;
}

// The payout whose commissions are shown below the list (the payout another tab sent the reader to, or the one they picked).
const shown = ref(props.focus ?? "");
const shownPayout = computed(() => list.data.value?.items.find((p) => p.id === shown.value));

function createAll() {
  create.mutate(undefined, {
    onSuccess: (r) => {
      if ((r.payouts ?? []).length === 0 && (r.skipped ?? []).length === 0) toast.info(t("affiliates.payouts.nothing"));
    },
  });
}
</script>

<template>
  <div class="flex flex-wrap items-end justify-between gap-3">
    <div>
      <h2 class="text-lg font-semibold">{{ t("affiliates.payouts.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("affiliates.payouts.intro") }}</p>
    </div>
    <Button type="button" :disabled="!canWrite || create.isPending.value" @click="createAll">{{ t("affiliates.payouts.create") }}</Button>
  </div>
  <ErrorText :error="create.error.value" />
  <ErrorText :error="cancel.error.value" />

  <Card v-if="result" data-testid="payout-result" role="status">
    <h3 class="text-base font-semibold">{{ t("affiliates.payouts.result") }}</h3>
    <p class="m-0 text-sm" data-testid="created-count">{{ t("affiliates.payouts.created", { count: (result.payouts ?? []).length }) }}</p>
    <ul v-if="(result.payouts ?? []).length" class="m-0 list-none p-0 text-sm">
      <li v-for="p in result.payouts" :key="p.id" class="tabular-nums">
        <span class="mono font-medium">{{ p.affiliate_code }}</span> · {{ fmt.amount(p.total_minor, p.currency) }}
      </li>
    </ul>
    <template v-if="(result.skipped ?? []).length">
      <p class="m-0 text-sm font-medium">{{ t("affiliates.payouts.skipped", { count: (result.skipped ?? []).length }) }}</p>
      <ul class="m-0 list-none p-0 text-sm" data-testid="skipped">
        <li v-for="(s, i) in result.skipped" :key="i" class="tabular-nums">
          <span class="mono font-medium">{{ codeOf(s.affiliate_id) }}</span> · {{ fmt.amount(s.total_minor, s.currency) }} ·
          {{ t(`affiliates.payouts.reasons.${s.reason}`) }}
        </li>
      </ul>
    </template>
  </Card>

  <Card class="p-3">
    <div class="flex flex-wrap items-center gap-2 px-3 pt-2">
      <NativeSelect v-model="status" class="w-44" :aria-label="t('affiliates.common.status')">
        <option value="">{{ t("affiliates.payouts.allStatuses") }}</option>
        <option v-for="s in ['open', 'paid', 'cancelled']" :key="s" :value="s">{{ t(`affiliates.status.payout.${s}`) }}</option>
      </NativeSelect>
    </div>
    <ErrorText :error="list.error.value" class="px-3" />
    <Skeleton v-if="list.isPending.value" class="mx-3 h-32" />
    <p v-else-if="list.isSuccess.value && !list.data.value?.items.length" class="text-muted-foreground px-3 py-4 text-sm">{{ t("affiliates.payouts.empty") }}</p>
    <Table v-else-if="list.data.value">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("affiliates.payouts.created_at") }}</TableHead>
          <TableHead>{{ t("affiliates.commissions.partner") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.payouts.total") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.payouts.count") }}</TableHead>
          <TableHead>{{ t("affiliates.payoutMethod") }}</TableHead>
          <TableHead>{{ t("affiliates.common.status") }}</TableHead>
          <TableHead>{{ t("affiliates.payouts.reference") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="p in list.data.value.items" :key="p.id" :data-payout="p.id" :data-state="shown === p.id ? 'selected' : undefined">
          <TableCell class="whitespace-nowrap">{{ fmt.date(p.create_time, "short") }}</TableCell>
          <TableCell class="mono font-medium">{{ p.affiliate_code }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.amount(p.total_minor, p.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.count(p.commission_count) }}</TableCell>
          <TableCell>{{ t(`affiliates.methods.${p.method ?? 'manual'}`) }}</TableCell>
          <TableCell>
            <div class="flex flex-wrap gap-1">
              <StatusPill :tone="payoutTone(p.status)">{{ t(`affiliates.status.payout.${p.status}`) }}</StatusPill>
              <StatusPill v-if="p.fake" tone="warning" data-testid="fake-badge">{{ t("affiliates.payouts.fake") }}</StatusPill>
            </div>
          </TableCell>
          <TableCell dir="auto" class="max-w-48 truncate">{{ p.reference || "—" }}</TableCell>
          <TableCell class="text-end">
            <div class="flex flex-wrap justify-end gap-1.5">
              <Button type="button" size="sm" variant="outline" :aria-pressed="shown === p.id" @click="shown = shown === p.id ? '' : (p.id ?? '')">{{ t("affiliates.payouts.commissions") }}</Button>
              <template v-if="p.status === 'open'">
                <Button type="button" size="sm" :disabled="!canWrite" @click="pay(p)">{{ t("affiliates.payouts.markPaid") }}</Button>
                <Button type="button" size="sm" variant="outline" :disabled="!canWrite || cancel.isPending.value" @click="cancel.mutate(p.id ?? '')">{{ t("affiliates.payouts.cancel") }}</Button>
              </template>
            </div>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.data.value" v-model:size="pager.size.value" :page="pager.page.value" :has-prev="pager.hasPrev.value" :has-next="!!list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>

  <section v-if="shown" :aria-label="t('affiliates.payouts.commissionsOf', { code: shownPayout?.affiliate_code ?? shown.slice(0, 8) })" class="flex flex-col gap-3">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="text-base font-semibold">{{ t("affiliates.payouts.commissionsOf", { code: shownPayout?.affiliate_code ?? shown.slice(0, 8) }) }}</h3>
      <Button type="button" size="sm" variant="outline" @click="shown = ''">{{ t("affiliates.common.close") }}</Button>
    </div>
    <p v-if="shownPayout?.fake" role="status" class="m-0 bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm font-medium">{{ t("affiliates.payouts.fakeNote") }}</p>
    <CommissionsTable :key="shown" :tenant-id="tenantId" :payout-id="shown" />
  </section>

  <MarkPaidDialog v-model:open="payOpen" :tenant-id="tenantId" :payout="paying" />
</template>
