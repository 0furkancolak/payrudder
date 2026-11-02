<script setup lang="ts">
import { ShieldBan, Trash2 } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { newest, oldest, time } from "@/shared/lib/sortDefs";
import { useTableState } from "@/shared/lib/useTableState";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { HeaderAction } from "@/shared/ui/page-header";
import { RowActions } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { SortSelect } from "@/shared/ui/sort-select";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { ref } from "vue";
import { KINDS, useAddBlocklistEntry, useAddPlatformBlocklistEntry, useBlocklist, usePlatformBlocklist, useRemoveBlocklistEntry, useRemovePlatformBlocklistEntry, type BlocklistEntry, type NewEntry } from "./api";
import BlocklistAddSheet from "./BlocklistAddSheet.vue";

/**
 * A blocklist: the e-mail addresses, tax ids and external customer ids that a new customer (or a customer's new address or tax id) may not match. The page is the app's own
 * list, or with `platform` the operator's, which is held against every app. At most 1000 entries, so the list is read whole and narrowed in the browser. A viewer sees the
 * list and is offered no control to change it. Customers that exist when an entry is added are not touched: they are blocked from the customer list.
 */
const props = defineProps<{ tenantId?: string; platform?: boolean }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();

// The two lists have the same shape; which hooks serve the page does not change while it is open.
const list = props.platform ? usePlatformBlocklist() : useBlocklist(() => props.tenantId ?? "");
const add = props.platform ? useAddPlatformBlocklistEntry() : useAddBlocklistEntry(() => props.tenantId ?? "");
const remove = props.platform ? useRemovePlatformBlocklistEntry() : useRemoveBlocklistEntry(() => props.tenantId ?? "");
const entries = computed<BlocklistEntry[]>(() => list.data.value?.entries ?? []);

const table = useTableState(entries, {
  searchFields: (e) => [e.value, e.note, e.created_by],
  sorts: () => [newest(t, (e) => time(e.create_time)), oldest(t, (e) => time(e.create_time))],
  url: true,
  filters: { kind: (e, v) => e.kind === v },
});
const kindFilter = computed({ get: () => table.filters.value.kind ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, kind: v }) });
const kindOptions = computed(() => KINDS.map((k) => ({ value: k, label: t(`blocklist.kinds.${k}`) })));

const adding = ref(false);
function save(entry: NewEntry) {
  add.mutate(entry, { onSuccess: () => (adding.value = false) });
}
async function take(e: BlocklistEntry) {
  if (!(await askConfirm({ body: t("blocklist.removeConfirm", { value: e.value }), destructive: true }))) return;
  remove.mutate(e.id);
}
</script>

<template>
  <HeaderAction v-if="canWrite" test-id="blocklist-new" :label="t('blocklist.new')" @click="adding = true" />
  <BlocklistAddSheet v-model:open="adding" :platform="platform" :pending="add.isPending.value" :error="add.error.value" @add="save" />
  <Card class="gap-0 p-3">
    <p class="text-muted-foreground px-3 pt-2 pb-3 text-sm">{{ platform ? t("blocklist.hintPlatform") : t("blocklist.hint") }}</p>
    <ErrorState :error="list.error.value" :retrying="list.isFetching.value" compact @retry="list.refetch()" />
    <ErrorText :error="remove.error.value" class="px-3 pb-2" />
    <DataToolbar v-if="list.isPending.value || entries.length > 0 || list.error.value" v-model:search="table.search.value" :placeholder="t('blocklist.search')" :active="table.active.value" @clear="table.clear()">
      <FilterSelect v-model="kindFilter" multiple :label="t('blocklist.columns.kind')" :options="kindOptions" />
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
    </DataToolbar>

    <div v-if="!list.isPending.value && entries.length === 0 && !list.error.value" class="flex flex-col items-center gap-3 px-4 py-14 text-center" data-testid="blocklist-empty">
      <span class="bg-brand-soft text-brand-strong inline-flex size-12 items-center justify-center rounded-2xl"><ShieldBan class="size-6" aria-hidden="true" /></span>
      <p class="m-0 text-base font-semibold">{{ t("blocklist.empty") }}</p>
      <p class="text-muted-foreground m-0 max-w-sm text-sm">{{ platform ? t("blocklist.emptyHintPlatform") : t("blocklist.emptyHint") }}</p>
    </div>

    <template v-else>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("blocklist.columns.value") }}</TableHead>
            <TableHead>{{ t("blocklist.columns.kind") }}</TableHead>
            <TableHead class="max-md:hidden">{{ t("blocklist.columns.note") }}</TableHead>
            <TableHead class="max-lg:hidden">{{ t("blocklist.columns.addedBy") }}</TableHead>
            <TableHead class="max-lg:hidden">{{ t("blocklist.columns.added") }}</TableHead>
            <TableHead v-if="canWrite" class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="list.isPending.value">
            <TableRow v-for="n in 4" :key="n" class="hover:bg-transparent"><TableCell colspan="6"><Skeleton class="h-4 w-full" /></TableCell></TableRow>
          </template>
          <TableRow v-for="e in table.items.value" :key="e.id" :data-testid="`blocklist-${e.kind}-${e.value}`">
            <TableCell class="mono max-w-0 min-w-0 truncate font-medium" dir="auto">{{ e.value }}</TableCell>
            <TableCell><StatusPill tone="neutral">{{ t(`blocklist.kinds.${e.kind}`) }}</StatusPill></TableCell>
            <TableCell class="text-muted-foreground max-w-xs truncate max-md:hidden" dir="auto">{{ e.note || "—" }}</TableCell>
            <TableCell class="text-muted-foreground max-lg:hidden">{{ e.created_by }}</TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap max-lg:hidden">{{ fmt.date(e.create_time, "day", "—") }}</TableCell>
            <TableCell v-if="canWrite" class="text-end">
              <RowActions :actions="[{ key: 'remove', label: t('blocklist.remove'), icon: Trash2, destructive: true, onSelect: () => take(e) }]" :label="`${t('common.moreActions')} ${e.value}`" />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="entries.length > 0 && table.total.value === 0" class="text-muted-foreground p-6 text-center text-sm">{{ t("common.noMatches") }}</p>
      <DataPagination v-if="entries.length > 0" v-model:size="table.size.value" :page="table.current.value" :pages="table.pages.value" :total="table.total.value" @update:page="table.page.value = $event" />
    </template>
  </Card>
</template>
