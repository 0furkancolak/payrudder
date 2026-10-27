<script setup lang="ts">
import { refDebounced } from "@vueuse/core";
import { Check, ArrowUpRight, Link2, Minus, Pencil, Users } from "@lucide/vue";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Plus } from "@lucide/vue";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useAffiliates, useBalances, useUpdateAffiliate } from "./api";
import AddAffiliateDialog from "./AddAffiliateDialog.vue";
import EditAffiliateDialog from "./EditAffiliateDialog.vue";
import LinkDialog from "./LinkDialog.vue";
import { affiliateTone, balancesByAffiliate, type Affiliate } from "./logic";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { useEdgeDetail } from "@/shared/lib/useEdgeDetail";
import { usePager } from "./paging";
import ReferralsDialog from "./ReferralsDialog.vue";

const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
const tenantId = computed(() => props.tenantId);

const status = ref("");
const search = ref("");
const searchNow = refDebounced(search, 300);
const pager = usePager();
watch([status, searchNow], () => pager.reset());

const list = useAffiliates(tenantId, () => ({ status: status.value, search: searchNow.value }), pager.token, pager.size);
const waiting = useAffiliates(tenantId, () => ({ status: "pending", search: "" }), "");
const balances = useBalances(tenantId);
const byPartner = computed(() => balancesByAffiliate(balances.data.value));
const update = useUpdateAffiliate(tenantId);

const addOpen = ref(false);
const dialog = ref<"" | "edit" | "link" | "referrals">("");
const selected = ref<Affiliate | null>(null);
function open(kind: "edit" | "link" | "referrals", a: Affiliate) {
  selected.value = a;
  dialog.value = kind;
}
const dialogOpen = (kind: string) => computed({ get: () => dialog.value === kind, set: (v: boolean) => (dialog.value = v ? (kind as "edit") : "") });
const editOpen = dialogOpen("edit");
const linkOpen = dialogOpen("link");
const referralsOpen = dialogOpen("referrals");

function setStatus(a: Affiliate, next: "active" | "suspended") {
  update.mutate({ id: a.id ?? "", body: { status: next } });
}

const statusOptions = computed(() => [
  { value: "", label: t("affiliates.partners.allStatuses") },
  ...["pending", "active", "suspended"].map((s) => ({ value: s, label: t(`affiliates.status.affiliate.${s}`) })),
]);
const items = computed(() => list.data.value?.items ?? []);
/** The sheet steps through the loaded page and, at its ends, loads the page beyond. */
const detail = useEdgeDetail(items, { hasPrev: pager.hasPrev, hasNext: () => !!list.data.value?.next, prev: pager.prev, next: () => pager.next(list.data.value?.next ?? "") });
const index = detail.index;
const current = detail.row;
/** The status change that fits the partner's status: approve a waiting one, suspend an active one, reinstate a suspended one. */
const statusStep = (a: Affiliate) => (a.status === "pending" ? { key: "approve", to: "active" as const } : a.status === "active" ? { key: "suspend", to: "suspended" as const } : { key: "reinstate", to: "active" as const });
const actionsOf = (a: Affiliate, i: number) => [
  { key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => (index.value = i) },
  { key: statusStep(a).key, label: t(`affiliates.partners.${statusStep(a).key}`), disabled: !canWrite.value || update.isPending.value, onSelect: () => setStatus(a, statusStep(a).to) },
  { key: "edit", label: t("affiliates.partners.edit"), icon: Pencil, disabled: !canWrite.value, onSelect: () => open("edit", a) },
  { key: "link", label: t("affiliates.partners.linkAndCode"), icon: Link2, onSelect: () => open("link", a) },
  { key: "referrals", label: t("affiliates.partners.referralsOf"), icon: Users, onSelect: () => open("referrals", a) },
];
/** The sheet closes before a dialog opens over it. */
function openFrom(kind: "edit" | "link" | "referrals", a: Affiliate) {
  index.value = null;
  open(kind, a);
}
</script>

<template>
  <div v-if="(waiting.data.value?.total ?? 0) > 0" data-testid="pending-banner" role="status" class="bg-brand-soft text-brand-strong rounded-2xl px-4 py-3 text-sm">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <span>{{ t("affiliates.partners.waiting", { count: waiting.data.value?.total ?? 0 }) }}</span>
      <Button v-if="status !== 'pending'" type="button" size="sm" variant="outline" @click="status = 'pending'">{{ t("affiliates.partners.showWaiting") }}</Button>
    </div>
  </div>

  <Card class="p-3">
    <DataToolbar v-model:search="search" :placeholder="t('affiliates.partners.search')" :active="status !== '' || search !== ''" class="pt-2" @clear="status = ''; search = ''">
      <FilterSelect v-model="status" :label="t('affiliates.common.status')" :options="statusOptions" />
      <template #actions>
        <Button type="button" :disabled="!canWrite" @click="addOpen = true"><Plus class="size-4" aria-hidden="true" />{{ t("affiliates.partners.add") }}</Button>
      </template>
    </DataToolbar>
    <ErrorText :error="list.error.value" class="px-3" />
    <ErrorText :error="update.error.value" class="px-3" />
    <Skeleton v-if="list.isPending.value" class="mx-3 h-32" />
    <p v-else-if="list.isSuccess.value && !list.data.value?.items.length" class="text-muted-foreground px-3 py-4 text-center text-sm">{{ t("affiliates.partners.empty") }}</p>
    <Table v-else-if="list.data.value">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("affiliates.partners.code") }}</TableHead>
          <TableHead>{{ t("affiliates.partners.partner") }}</TableHead>
          <TableHead>{{ t("affiliates.common.status") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.partners.clicks") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.partners.referrals") }}</TableHead>
          <TableHead>{{ t("affiliates.partners.signedIn") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.partners.balance") }}</TableHead>
          <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(a, i) in list.data.value.items" :key="a.id" :data-affiliate="a.code" class="cursor-pointer" :data-state="index === i ? 'selected' : undefined" @click="index = i">
          <TableCell class="mono font-medium">{{ a.code }}</TableCell>
          <TableCell class="min-w-0">
            <div class="font-medium">{{ a.name || a.email }}</div>
            <div v-if="a.name" class="text-muted-foreground text-xs" dir="auto">{{ a.email }}</div>
            <div v-if="a.source === 'application'" class="text-muted-foreground text-xs">{{ t("affiliates.partners.applied") }}</div>
          </TableCell>
          <TableCell><StatusPill :tone="affiliateTone(a.status)">{{ t(`affiliates.status.affiliate.${a.status}`) }}</StatusPill></TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.count(Number(a.clicks ?? 0)) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.count(Number(a.referrals ?? 0)) }}</TableCell>
          <TableCell>
            <Check v-if="a.signed_in" class="text-success size-4" aria-hidden="true" /><Minus v-else class="text-muted-foreground size-4" aria-hidden="true" />
            <span class="sr-only">{{ a.signed_in ? t("affiliates.common.yes") : t("affiliates.common.no") }}</span>
          </TableCell>
          <TableCell class="text-end text-xs tabular-nums">
            <template v-if="byPartner.get(a.id ?? '')?.length">
              <div v-for="b in byPartner.get(a.id ?? '')" :key="b.currency">
                {{ t("affiliates.partners.available", { amount: fmt.amount(b.approved_minor, b.currency) }) }}
                <div class="text-muted-foreground">{{ t("affiliates.partners.paidAmount", { amount: fmt.amount(b.paid_minor, b.currency) }) }}</div>
              </div>
            </template>
            <span v-else class="text-muted-foreground">—</span>
          </TableCell>
          <TableCell class="text-end"><RowActions :actions="actionsOf(a, i)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.data.value" v-model:size="pager.size.value" :page="pager.page.value" :has-prev="pager.hasPrev.value" :has-next="!!list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>

  <DetailSheet v-model:index="index" :total="items.length" :offset="pager.offset.value" :of="list.data.value?.total" :edge="detail.edge.value" :title="current?.name || current?.email || current?.code || ''" :description="current?.code" @edge="detail.onEdge">
    <dl v-if="current" class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm" data-testid="partner-detail">
      <dt class="text-muted-foreground">{{ t("affiliates.common.status") }}</dt>
      <dd class="flex justify-end"><StatusPill :tone="affiliateTone(current.status)">{{ t(`affiliates.status.affiliate.${current.status}`) }}</StatusPill></dd>
      <dt class="text-muted-foreground">{{ t("affiliates.partners.partner") }}</dt>
      <dd class="text-end" dir="auto">{{ current.name || "—" }}<span class="text-muted-foreground block text-xs">{{ current.email }}</span></dd>
      <dt class="text-muted-foreground">{{ t("affiliates.partners.clicks") }}</dt>
      <dd class="text-end tabular-nums">{{ fmt.count(Number(current.clicks ?? 0)) }}</dd>
      <dt class="text-muted-foreground">{{ t("affiliates.partners.referrals") }}</dt>
      <dd class="text-end tabular-nums">{{ fmt.count(Number(current.referrals ?? 0)) }}</dd>
      <dt class="text-muted-foreground">{{ t("affiliates.partners.signedIn") }}</dt>
      <dd class="text-end">{{ current.signed_in ? t("affiliates.common.yes") : t("affiliates.common.no") }}</dd>
      <dt class="text-muted-foreground">{{ t("affiliates.partners.balance") }}</dt>
      <dd class="text-end text-xs tabular-nums">
        <template v-if="byPartner.get(current.id ?? '')?.length">
          <div v-for="b in byPartner.get(current.id ?? '')" :key="b.currency">
            {{ t("affiliates.partners.available", { amount: fmt.amount(b.approved_minor, b.currency) }) }}
            <div class="text-muted-foreground">{{ t("affiliates.partners.paidAmount", { amount: fmt.amount(b.paid_minor, b.currency) }) }}</div>
          </div>
        </template>
        <span v-else class="text-muted-foreground">—</span>
      </dd>
    </dl>
    <template v-if="current" #footer>
      <Button type="button" variant="outline" @click="openFrom('referrals', current)">{{ t("affiliates.partners.referralsOf") }}</Button>
      <Button type="button" variant="outline" @click="openFrom('link', current)">{{ t("affiliates.partners.linkAndCode") }}</Button>
      <Button type="button" variant="outline" :disabled="!canWrite" @click="openFrom('edit', current)">{{ t("affiliates.partners.edit") }}</Button>
    </template>
  </DetailSheet>

  <AddAffiliateDialog v-model:open="addOpen" :tenant-id="tenantId" />
  <EditAffiliateDialog v-model:open="editOpen" :tenant-id="tenantId" :affiliate="selected" />
  <LinkDialog v-model:open="linkOpen" :tenant-id="tenantId" :affiliate="selected" />
  <ReferralsDialog v-model:open="referralsOpen" :tenant-id="tenantId" :affiliate="selected" />
</template>
