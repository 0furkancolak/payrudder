<script setup lang="ts">
import { ArrowUpRight, Download, Loader2 } from "@lucide/vue";
import { computed, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { useEdgeDetail } from "@/shared/lib/useEdgeDetail";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataToolbar } from "@/shared/ui/data-table";
import { DateRangePicker } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { DetailList, type DetailRow } from "@/shared/ui/detail-list";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Input } from "@/shared/ui/input";
import { RowActions } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useAuditEvents, useExportAudit, type AuditSource } from "./api";
import { AUDIT_AREAS, actionLabelKey, actorLine, emptyFilter, isFiltered, prettyDetail, targetLine, type ActorType, type AuditEvent } from "./lib";

/**
 * The audit log: who did what and when, newest first, for one tenant (`mode="tenant"`) or, for the operator, across all of them (`mode="operator"`, with a
 * tenant filter). A row opens a sheet with every field of the entry. Following the console's list contract (docs/13-admin-ui.md): the area and the dates
 * wait for their Apply button, the actor is a search that goes to the server and applies when its box is left or Enter is pressed, there is no sort (the
 * server's order is the log's order) and no bulk action (an entry cannot be changed). Pages are appended with "Load more"; the CSV is the server's own export
 * of the same filter.
 */
const props = defineProps<{ mode: "tenant" | "operator"; tenantId?: string }>();
const { t, te } = useI18n();
const fmt = useFormat();

const filter = reactive(emptyFilter());
// What is typed in the text boxes; it becomes part of the filter when the box is left or Enter is pressed.
const actorDraft = ref("");
const tenantDraft = ref("");
const source = computed<AuditSource>(() => (props.mode === "operator" ? { kind: "operator" } : { kind: "tenant", tenantId: props.tenantId ?? "" }));
const log = useAuditEvents(source, filter);
const exporter = useExportAudit(source);

const rows = computed(() => (log.data.value?.pages ?? []).flatMap((p) => p.events ?? []));
const filtering = computed(() => isFiltered(filter));
/** A service that keeps part of the log did not answer: the entries on the page may lack its. */
const unavailable = computed(() => [...new Set((log.data.value?.pages ?? []).flatMap((p) => (p.incomplete ? p.unavailable_sources : [])))]);
const sourceName = (s: string) => (te(`audit.sources.${s}`) ? t(`audit.sources.${s}`) : s);

const areaOptions = computed(() => [{ value: "", label: t("audit.filters.anyArea") }, ...AUDIT_AREAS.map((a) => ({ value: a, label: t(`audit.areas.${a}`) }))]);
const applyActor = () => (filter.actor = actorDraft.value.trim());
const applyTenant = () => (filter.tenant = tenantDraft.value.trim());
function clear() {
  Object.assign(filter, emptyFilter());
  actorDraft.value = tenantDraft.value = "";
}

// The entry whose sheet is open: an index into the entries loaded, named in the address so a reload or a shared link finds it again (further pages are
// loaded to look for it).
const nav = useEdgeDetail(rows, { hasPrev: false, hasNext: () => !!log.hasNextPage.value, prev: () => undefined, next: () => log.fetchNextPage() }, { kind: "audit", id: (e) => e.id, ready: () => log.isSuccess.value });
const shown = nav.index;
const current = nav.row;

const label = (action: string) => {
  const key = actionLabelKey(action);
  return key && te(key) ? t(key) : action;
};
const TONES: Record<ActorType, "info" | "neutral" | "warning"> = { user: "info", api_key: "neutral", operator: "warning", system: "neutral" };
const actorType = (e: AuditEvent) => t(`audit.actorTypes.${e.actor_type}`);
const when = (e: AuditEvent) => fmt.date(e.create_time, "dateTime");
const detail = computed(() => (current.value ? prettyDetail(current.value.detail_json) : ""));

const sheetRows = computed<DetailRow[]>(() => {
  const e = current.value;
  if (!e) return [];
  return [
    { key: "summary", label: t("audit.fields.summary"), value: e.summary },
    { key: "time", label: t("audit.fields.time"), value: when(e) },
    { key: "actor", label: t("audit.fields.actor"), value: e.actor_email },
    { key: "actorId", label: t("audit.fields.actorId"), value: e.actor_id, mono: true },
    { key: "target", label: t("audit.fields.target"), value: e.target_type },
    { key: "targetId", label: t("audit.fields.targetId"), value: e.target_id, mono: true },
    { key: "ip", label: t("audit.fields.ip"), value: e.source_ip, mono: true },
    { key: "request", label: t("audit.fields.request"), value: e.request_id, mono: true },
    ...(props.mode === "operator" ? [{ key: "tenant", label: t("audit.fields.tenant"), value: e.tenant_id, mono: true }, { key: "account", label: t("audit.fields.account"), value: e.account_id, mono: true }] : []),
    { key: "before", label: t("audit.fields.before"), value: e.before_hash, mono: true },
    { key: "after", label: t("audit.fields.after"), value: e.after_hash, mono: true },
  ];
});
</script>

<template>
  <Card class="p-3" role="region" :aria-label="t('audit.title')">
    <DataToolbar v-model:search="actorDraft" :placeholder="t('audit.filters.actor')" :active="filtering" @clear="clear" @focusout="applyActor" @keydown.enter="applyActor">
      <FilterSelect v-model="filter.areas" multiple :label="t('audit.filters.area')" :options="areaOptions" />
      <DateRangePicker v-model="filter.range" />
      <Input
        v-if="mode === 'operator'"
        :model-value="tenantDraft"
        class="mono w-72 max-w-full"
        :placeholder="t('audit.filters.tenant')"
        :aria-label="t('audit.filters.tenant')"
        spellcheck="false"
        data-testid="audit-tenant"
        @update:model-value="(v) => (tenantDraft = String(v))"
        @change="applyTenant"
      />
      <template #actions>
        <Button type="button" variant="outline" :disabled="exporter.isPending.value" data-testid="audit-export" @click="exporter.mutate({ ...filter })">
          <Loader2 v-if="exporter.isPending.value" class="animate-spin" aria-hidden="true" /><Download v-else aria-hidden="true" />{{ t("audit.export") }}
        </Button>
      </template>
    </DataToolbar>

    <ErrorText :error="exporter.error.value" box class="mx-3 mb-3" />
    <ErrorState :error="log.error.value" :retrying="log.isFetching.value" compact @retry="log.refetch()" />
    <p v-if="unavailable.length > 0" class="bg-warning-soft text-warning mx-3 mb-3 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="audit-incomplete">
      {{ t("audit.incomplete", { sources: unavailable.map(sourceName).join(", ") }) }}
    </p>

    <Skeleton v-if="log.isPending.value && !log.error.value" class="mx-3 h-40 rounded-2xl" />
    <template v-else-if="!log.error.value || rows.length > 0">
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("audit.columns.time") }}</TableHead>
            <TableHead>{{ t("audit.columns.actor") }}</TableHead>
            <TableHead>{{ t("audit.columns.action") }}</TableHead>
            <TableHead>{{ t("audit.columns.summary") }}</TableHead>
            <TableHead class="max-lg:hidden">{{ t("audit.columns.target") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(e, i) in rows" :key="e.id" class="cursor-pointer" :data-testid="`audit-row-${e.id}`" @click="shown = i">
            <TableCell class="whitespace-nowrap tabular-nums">{{ when(e) }}</TableCell>
            <TableCell>
              <div class="flex flex-wrap items-center gap-2">
                <StatusPill :tone="TONES[e.actor_type] ?? 'neutral'">{{ actorType(e) }}</StatusPill>
                <span class="min-w-0 break-all text-sm">{{ actorLine(e) }}</span>
              </div>
            </TableCell>
            <TableCell class="font-medium">{{ label(e.action) }}</TableCell>
            <TableCell class="text-muted-foreground max-w-md text-sm">{{ e.summary }}</TableCell>
            <TableCell class="mono text-muted-foreground text-xs break-all max-lg:hidden">{{ targetLine(e) }}</TableCell>
            <TableCell><RowActions :actions="[{ key: 'view', label: t('common.view'), icon: ArrowUpRight, onSelect: () => (shown = i) }]" :label="`${t('common.moreActions')} ${label(e.action)}`" /></TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <TableEmpty v-if="rows.length === 0" data-testid="audit-empty">{{ t(filtering ? "common.noMatches" : "audit.empty") }}</TableEmpty>
      <div v-if="log.hasNextPage.value" class="flex justify-center p-3">
        <Button type="button" variant="outline" :disabled="log.isFetchingNextPage.value" data-testid="audit-more" @click="log.fetchNextPage()">
          <Loader2 v-if="log.isFetchingNextPage.value" class="animate-spin" aria-hidden="true" />{{ t("audit.loadMore") }}
        </Button>
      </div>
    </template>
  </Card>

  <DetailSheet v-model:index="shown" :total="rows.length" :title="current ? label(current.action) : ''" :description="current ? when(current) : ''" wide>
    <div v-if="current" class="flex flex-col gap-5" data-testid="audit-detail">
      <div class="flex flex-wrap items-center gap-2">
        <StatusPill :tone="TONES[current.actor_type] ?? 'neutral'">{{ actorType(current) }}</StatusPill>
        <span class="mono text-muted-foreground text-xs break-all">{{ current.action }}</span>
      </div>
      <DetailList :rows="sheetRows" />
      <section v-if="detail" :aria-label="t('audit.fields.detail')">
        <h3 class="mb-2 text-sm font-medium">{{ t("audit.fields.detail") }}</h3>
        <pre class="bg-muted mono max-h-96 overflow-auto rounded-2xl p-4 text-xs break-words whitespace-pre-wrap" dir="ltr" data-testid="audit-detail-json">{{ detail }}</pre>
      </section>
    </div>
  </DetailSheet>
</template>
