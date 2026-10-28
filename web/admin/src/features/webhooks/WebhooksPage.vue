<script setup lang="ts">
import { HeaderAction } from "@/shared/ui/page-header";
import { askConfirm } from "@/shared/lib/confirm";
import { useParams } from "@tanstack/vue-router";
import AppField from "@/app/layout/AppField.vue";
import { ListTree, Pause, Play, RefreshCw, Send, Trash2 } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useShell } from "@/app/layout/useShell";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { firstError } from "@/shared/lib/errors";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { FilterSelect } from "@/shared/ui/filter-select";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { RowActions, type RowAction } from "@/shared/ui/row-actions";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { answerBody, isInsecureUrl, plainAnswer } from "./lib";
import { EVENT_TYPES, useWebhookActions, useWebhookDeliveries, useWebhookEndpoints, isEventType, type EventType } from "./api";

const { t } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const endpoints = useWebhookEndpoints(tenantId);
const act = useWebhookActions(tenantId);
const shell = useShell();
/** A plain http endpoint is accepted, but what the person should know about it depends on the environment the endpoint is in. */
const insecureNote = (u: string | undefined) => (isInsecureUrl(u) ? t(shell.env.value === "sandbox" ? "billing.webhooks.insecure.sandbox" : "billing.webhooks.insecure.live") : "");
const answer = (d: { last_error?: string; last_status?: number | string }) => plainAnswer(answerBody(d.last_error, d.last_status));

const creating = ref(false);
const url = ref("");
const description = ref("");
const types = ref<EventType[]>([]);
const selected = ref("");
const deliveries = useWebhookDeliveries(tenantId, selected);
const list = computed(() => endpoints.data.value?.endpoints ?? []);
const range = ref<DateRange>(thisMonth());
const log = computed(() => (deliveries.data.value?.deliveries ?? []).filter((d) => inRange(d.create_time, range.value)));
const endpointsTable = useTableState(list, {
  sheet: { kind: "endpoint", id: (e) => e.id }, searchFields: (e) => [e.url, e.description, ...(e.event_types ?? [])],
  url: true, filters: { status: (e, v) => (e.active ? "active" : "paused") === v },
});
const deliveriesTable = useTableState(log, {
  sheet: { kind: "delivery", id: (d) => d.id }, searchFields: (d) => [d.event_type, d.last_error, d.last_status],
  filters: { state: (d, v) => d.state === v },
});
const current = endpointsTable.row;
watch(
  () => current.value?.id,
  (id) => {
    selected.value = id ?? "";
    deliveriesTable.clear();
    range.value = { from: "", to: "" };
  },
);
const deliveriesActive = computed(() => deliveriesTable.active.value || range.value.from !== "" || range.value.to !== "");
function clearDeliveries() {
  deliveriesTable.clear();
  range.value = { from: "", to: "" };
}
type Endpoint = (typeof list.value)[number];
function actionsOf(e: Endpoint, i: number): RowAction[] {
  return [
    { key: "deliveries", label: t("billing.webhooks.deliveries"), icon: ListTree, onSelect: () => endpointsTable.open(i) },
    { key: "test", label: t("billing.webhooks.test"), icon: Send, disabled: act.test.isPending.value, onSelect: () => act.test.mutate(e.id ?? "") },
    { key: "toggle", label: e.active ? t("billing.webhooks.pause") : t("billing.webhooks.resume"), icon: e.active ? Pause : Play, onSelect: () => toggle(e) },
    { key: "rotate", label: t("billing.webhooks.rotate"), icon: RefreshCw, onSelect: () => rotate(e.id) },
    { key: "delete", label: t("billing.webhooks.delete"), icon: Trash2, destructive: true, separatorBefore: true, onSelect: () => remove(e.id) },
  ];
}
const deliveryStates = computed(() => [...new Set(log.value.map((d) => d.state as string | undefined).filter((s): s is string => !!s))].sort());
/** A secret is shown right after it is made and never again. */
const secret = computed(() => ((act.create.data.value ?? act.rotate.data.value) as { secret?: string } | undefined)?.secret ?? "");
const when = (v?: string | null) => fmt.date(v, "shortSeconds", "—");
const stateVariant = (s?: string) => (s === "WEBHOOK_DELIVERY_STATE_DELIVERED" ? "success" : s === "WEBHOOK_DELIVERY_STATE_FAILED" ? "destructive" : "secondary");
const stateWord = (s?: string) => t(`billing.webhooks.state.${(s ?? "").replace("WEBHOOK_DELIVERY_STATE_", "").toLowerCase()}`);
const failure = computed(() => firstError(...Object.values(act)));

// A sheet that was closed opens clean: the last values and error are not shown again.
watch(creating, (v) => {
  if (v) {
    url.value = description.value = "";
    types.value = [];
    act.create.reset();
  }
});

function create() {
  act.create.mutate({ url: url.value.trim(), description: description.value.trim(), event_types: [...types.value] }, { onSuccess: () => { url.value = description.value = ""; types.value = []; creating.value = false; } });
}
async function remove(id: string | undefined) {
  if (id && (await askConfirm({ title: t("billing.webhooks.deleteTitle"), body: t("billing.webhooks.confirmDelete"), confirmLabel: t("billing.webhooks.delete") }))) {
    act.remove.mutate(id, { onSuccess: () => { if (current.value?.id === id) endpointsTable.detail.value = null; } });
  }
}
/** Rotating cuts the old secret off at once: every receiver that still verifies with it fails until it is given the new one. */
async function rotate(id: string | undefined) {
  if (id && (await askConfirm({ title: t("billing.webhooks.rotateTitle"), body: t("billing.webhooks.confirmRotate"), confirmLabel: t("billing.webhooks.rotate") }))) act.rotate.mutate(id);
}
function toggle(e: { id?: string; url?: string; description?: string; event_types?: string[]; active?: boolean }) {
  act.update.mutate({ id: e.id ?? "", url: e.url ?? "", description: e.description ?? "", event_types: (e.event_types ?? []).filter(isEventType), active: !e.active });
}
</script>

<template>
  <Sheet v-model:open="creating" name="new-endpoint">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.webhooks.add") }}</SheetTitle>
        <SheetDescription>{{ t("billing.webhooks.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-webhook" class="grid gap-3" role="form" :aria-label="t('billing.webhooks.formLabel')" @submit.prevent="create">
          <AppField />
          <div class="grid gap-1.5">
            <Label for="webhook-url">{{ t("billing.webhooks.url") }}</Label>
            <Input id="webhook-url" v-model="url" type="url" required placeholder="https://example.com/payrudder-webhook" :aria-describedby="insecureNote(url) ? 'webhook-url-note' : undefined" />
            <p v-if="insecureNote(url)" id="webhook-url-note" class="text-warning text-xs" role="status" data-testid="webhook-insecure">{{ insecureNote(url) }}</p>
          </div>
          <div class="grid gap-1.5">
            <Label for="webhook-description">{{ t("billing.webhooks.description") }}</Label>
            <Input id="webhook-description" v-model="description" maxlength="200" />
          </div>
          <fieldset class="grid gap-1">
            <legend class="text-muted-foreground mb-1 text-sm">{{ t("billing.webhooks.events") }}</legend>
            <label v-for="type in EVENT_TYPES" :key="type" class="mono flex items-center gap-2 text-xs">
              <input v-model="types" type="checkbox" :value="type" class="size-4" />{{ type }}
            </label>
          </fieldset>
          <p class="text-muted-foreground text-xs">{{ t("billing.webhooks.eventsHint") }}</p>
          <ErrorText :error="act.create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="creating = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-webhook" :disabled="act.create.isPending.value">{{ t("billing.webhooks.add") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <p class="text-muted-foreground mb-3 max-w-3xl px-1 text-sm">{{ t("billing.webhooks.hint") }}</p>
  <div v-if="secret" class="border-warning/50 bg-warning/10 mb-3 rounded-2xl border p-3" role="status">
    <p class="text-sm font-medium">{{ t("billing.webhooks.secretOnce") }}</p>
    <p class="mono break-all text-sm" data-testid="webhook-secret">{{ secret }}</p>
  </div>
  <ErrorText :error="failure" class="mb-3" />
  <ErrorText :error="endpoints.error.value" class="mb-3" />

  <Card class="mb-6 p-3">
    <DataToolbar v-model:search="endpointsTable.search.value" :active="endpointsTable.active.value" @clear="endpointsTable.clear()">
      <FilterSelect v-model="endpointsTable.filters.value.status" multiple :label="t('billing.webhooks.status')" :options="[{ value: '', label: t('billing.webhooks.allStatuses') }, { value: 'active', label: t('billing.webhooks.active') }, { value: 'paused', label: t('billing.webhooks.paused') }]" />
      <HeaderAction data-testid="webhooks-new" :label="t('billing.webhooks.add')" @click="creating = true" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.webhooks.url") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("billing.webhooks.events") }}</TableHead>
          <TableHead>{{ t("billing.webhooks.status") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(e, i) in endpointsTable.items.value" :key="e.id" class="cursor-pointer" @click="endpointsTable.open(i)">
          <TableCell>
            <span class="mono text-sm break-all">{{ e.url }}</span>
            <span v-if="insecureNote(e.url)" class="text-warning block text-xs" data-testid="webhook-insecure-row">{{ insecureNote(e.url) }}</span>
            <span v-if="e.description" class="text-muted-foreground block text-xs">{{ e.description }}</span>
          </TableCell>
          <TableCell class="max-w-72 text-xs whitespace-normal max-md:hidden">{{ e.event_types?.length ? e.event_types.join(", ") : t("billing.webhooks.allEvents") }}</TableCell>
          <TableCell><Badge :variant="e.active ? 'success' : 'secondary'">{{ e.active ? t("billing.webhooks.active") : t("billing.webhooks.paused") }}</Badge></TableCell>
          <TableCell><RowActions :actions="actionsOf(e, i)" :label="`${t('common.moreActions')} ${e.url}`" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!endpoints.isPending.value && !endpoints.error.value && list.length === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.webhooks.empty") }}</p>
    <p v-else-if="list.length > 0 && endpointsTable.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="endpointsTable.page.value" v-model:size="endpointsTable.size.value" :pages="endpointsTable.pages.value" :total="endpointsTable.total.value" />
  </Card>

  <DetailSheet v-model:index="endpointsTable.detail.value" :total="endpointsTable.total.value" :title="current?.url ?? ''" :description="current?.description || undefined" wide>
    <div v-if="current" class="flex flex-col gap-5" role="region" :aria-label="t('billing.webhooks.deliveries')">
      <p v-if="insecureNote(current.url)" class="text-warning text-xs" role="status">{{ insecureNote(current.url) }}</p>
      <div class="flex flex-wrap items-center gap-2 text-sm">
        <Badge :variant="current.active ? 'success' : 'secondary'">{{ current.active ? t("billing.webhooks.active") : t("billing.webhooks.paused") }}</Badge>
        <span class="text-muted-foreground text-xs">{{ current.event_types?.length ? current.event_types.join(", ") : t("billing.webhooks.allEvents") }}</span>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button size="sm" variant="outline" :disabled="act.test.isPending.value" @click="act.test.mutate(current.id ?? '')"><Send class="size-4" aria-hidden="true" />{{ t("billing.webhooks.test") }}</Button>
        <Button size="sm" variant="outline" @click="toggle(current)"><component :is="current.active ? Pause : Play" class="size-4" aria-hidden="true" />{{ current.active ? t("billing.webhooks.pause") : t("billing.webhooks.resume") }}</Button>
        <Button size="sm" variant="outline" @click="rotate(current.id)"><RefreshCw class="size-4" aria-hidden="true" />{{ t("billing.webhooks.rotate") }}</Button>
        <Button size="sm" variant="outline" class="text-destructive ms-auto" data-testid="webhook-sheet-delete" @click="remove(current.id)"><Trash2 class="size-4" aria-hidden="true" />{{ t("billing.webhooks.delete") }}</Button>
      </div>
      <h3 class="text-base font-semibold">{{ t("billing.webhooks.deliveries") }}</h3>
      <div class="-mx-3">
        <DataToolbar v-model:search="deliveriesTable.search.value" :active="deliveriesActive" @clear="clearDeliveries()">
          <FilterSelect v-model="deliveriesTable.filters.value.state" multiple :label="t('billing.webhooks.status')" :options="[{ value: '', label: t('billing.webhooks.allStatuses') }, ...deliveryStates.map((s) => ({ value: s, label: stateWord(s) }))]" />
          <DateRangePicker v-model="range" />
        </DataToolbar>
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <TableHead>{{ t("billing.webhooks.event") }}</TableHead>
              <TableHead>{{ t("billing.webhooks.status") }}</TableHead>
              <TableHead class="text-end">{{ t("billing.webhooks.attempts") }}</TableHead>
              <TableHead>{{ t("billing.webhooks.lastAnswer") }}</TableHead>
              <TableHead>{{ t("billing.webhooks.date") }}</TableHead>
              <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="d in deliveriesTable.items.value" :key="d.id">
              <TableCell class="mono text-xs break-all">{{ d.event_type }}</TableCell>
              <TableCell>
                <Badge :variant="stateVariant(d.state)">{{ stateWord(d.state) }}</Badge>
                <span v-if="d.state === 'WEBHOOK_DELIVERY_STATE_PENDING' && d.attempts" class="text-muted-foreground block text-xs">{{ t("billing.webhooks.nextTry", { when: when(d.next_attempt_time) }) }}</span>
              </TableCell>
              <TableCell class="px-2 text-end tabular-nums">{{ d.attempts }}</TableCell>
              <TableCell class="max-w-24 text-xs" data-testid="delivery-answer">
                <div class="flex items-center gap-1.5">
                  <Badge v-if="d.last_status" :variant="Number(d.last_status) < 300 ? 'success' : 'destructive'" class="shrink-0">{{ d.last_status }}</Badge>
                  <span class="min-w-0 truncate" :title="answer(d).full || undefined">{{ answer(d).text }}</span>
                </div>
              </TableCell>
              <TableCell class="max-w-24 text-xs">{{ when(d.create_time) }}</TableCell>
              <TableCell><RowActions :actions="[{ key: 'redeliver', label: t('billing.webhooks.redeliver'), disabled: act.redeliver.isPending.value, onSelect: () => act.redeliver.mutate(d.id ?? '') }]" /></TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <p v-if="!deliveries.isPending.value && !deliveries.error.value && (deliveries.data.value?.deliveries ?? []).length === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.webhooks.noDeliveries") }}</p>
        <p v-else-if="log.length === 0 || deliveriesTable.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
        <DataPagination v-if="log.length > 0" v-model:page="deliveriesTable.page.value" v-model:size="deliveriesTable.size.value" :pages="deliveriesTable.pages.value" :total="deliveriesTable.total.value" />
      </div>
    </div>
  </DetailSheet>
</template>
