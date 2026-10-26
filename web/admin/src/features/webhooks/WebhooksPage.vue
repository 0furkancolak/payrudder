<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { Plus } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { firstError } from "@/shared/lib/errors";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { EVENT_TYPES, useWebhookActions, useWebhookDeliveries, useWebhookEndpoints, isEventType, type EventType } from "./api";

const { t } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const endpoints = useWebhookEndpoints(tenantId);
const act = useWebhookActions(tenantId);

const creating = ref(false);
const url = ref("");
const description = ref("");
const types = ref<EventType[]>([]);
const selected = ref("");
const deliveries = useWebhookDeliveries(tenantId, selected);
const list = computed(() => endpoints.data.value?.endpoints ?? []);
const log = computed(() => deliveries.data.value?.deliveries ?? []);
const endpointsTable = useTableState(list, {
  searchFields: (e) => [e.url, e.description, ...(e.event_types ?? [])],
  filters: { status: (e, v) => (e.active ? "active" : "paused") === v },
});
const deliveriesTable = useTableState(log, {
  searchFields: (d) => [d.event_type, d.last_error, d.last_status],
  filters: { state: (d, v) => d.state === v },
});
watch(selected, () => deliveriesTable.clear());
const deliveryStates = computed(() => [...new Set(log.value.map((d) => d.state as string | undefined).filter((s): s is string => !!s))].sort());
/** A secret is shown right after it is made and never again. */
const secret = computed(() => ((act.create.data.value ?? act.rotate.data.value) as { secret?: string } | undefined)?.secret ?? "");
const when = (v?: string) => fmt.date(v, "shortSeconds", "—");
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
function remove(id: string | undefined) {
  if (id && window.confirm(t("billing.webhooks.confirmDelete"))) act.remove.mutate(id);
}
function toggle(e: { id?: string; url?: string; description?: string; event_types?: string[]; active?: boolean }) {
  act.update.mutate({ id: e.id ?? "", url: e.url ?? "", description: e.description ?? "", event_types: (e.event_types ?? []).filter(isEventType), active: !e.active });
}
</script>

<template>
  <Sheet v-model:open="creating">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.webhooks.add") }}</SheetTitle>
        <SheetDescription>{{ t("billing.webhooks.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-webhook" class="grid gap-3" role="form" :aria-label="t('billing.webhooks.formLabel')" @submit.prevent="create">
          <Input v-model="url" type="url" required :aria-label="t('billing.webhooks.url')" placeholder="https://example.com/payrudder-webhook" />
          <Input v-model="description" maxlength="200" :aria-label="t('billing.webhooks.description')" :placeholder="t('billing.webhooks.description')" />
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

  <Card class="mb-6">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold">{{ t("billing.webhooks.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("billing.webhooks.hint") }}</p>
      </div>
      <Button data-testid="webhooks-new" @click="creating = true"><Plus class="size-4" />{{ t("billing.webhooks.add") }}</Button>
    </div>
    <div v-if="secret" class="border-warning/50 bg-warning/10 rounded-md border p-3" role="status">
      <p class="text-sm font-medium">{{ t("billing.webhooks.secretOnce") }}</p>
      <p class="mono break-all text-sm" data-testid="webhook-secret">{{ secret }}</p>
    </div>
    <ErrorText :error="failure" />
    <ErrorText :error="endpoints.error.value" />
  </Card>

  <Card class="mb-6 p-3">
    <DataToolbar v-model:search="endpointsTable.search.value" :active="endpointsTable.active.value" @clear="endpointsTable.clear()">
      <NativeSelect v-model="endpointsTable.filters.value.status" class="w-40" :aria-label="t('billing.webhooks.status')">
        <option value="">{{ t("billing.webhooks.allStatuses") }}</option>
        <option value="active">{{ t("billing.webhooks.active") }}</option>
        <option value="paused">{{ t("billing.webhooks.paused") }}</option>
      </NativeSelect>
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.webhooks.url") }}</TableHead>
          <TableHead>{{ t("billing.webhooks.events") }}</TableHead>
          <TableHead>{{ t("billing.webhooks.status") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.webhooks.actions") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="e in endpointsTable.items.value" :key="e.id">
          <TableCell>
            <span class="mono text-sm break-all">{{ e.url }}</span>
            <span v-if="e.description" class="text-muted-foreground block text-xs">{{ e.description }}</span>
          </TableCell>
          <TableCell class="text-xs">{{ e.event_types?.length ? e.event_types.join(", ") : t("billing.webhooks.allEvents") }}</TableCell>
          <TableCell><Badge :variant="e.active ? 'success' : 'secondary'">{{ e.active ? t("billing.webhooks.active") : t("billing.webhooks.paused") }}</Badge></TableCell>
          <TableCell class="space-x-1 text-end whitespace-nowrap">
            <Button size="sm" variant="outline" @click="selected = e.id ?? ''">{{ t("billing.webhooks.deliveries") }}</Button>
            <Button size="sm" variant="outline" :disabled="act.test.isPending.value" @click="act.test.mutate(e.id ?? '')">{{ t("billing.webhooks.test") }}</Button>
            <Button size="sm" variant="outline" @click="toggle(e)">{{ e.active ? t("billing.webhooks.pause") : t("billing.webhooks.resume") }}</Button>
            <Button size="sm" variant="outline" @click="act.rotate.mutate(e.id ?? '')">{{ t("billing.webhooks.rotate") }}</Button>
            <Button size="sm" variant="destructive" @click="remove(e.id)">{{ t("billing.webhooks.delete") }}</Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!endpoints.isPending.value && list.length === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.webhooks.empty") }}</p>
    <p v-else-if="list.length > 0 && endpointsTable.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="endpointsTable.page.value" v-model:size="endpointsTable.size.value" :pages="endpointsTable.pages.value" :total="endpointsTable.total.value" />
  </Card>

  <Card v-if="selected" class="p-3" role="region" :aria-label="t('billing.webhooks.deliveries')">
    <h2 class="px-3 pt-2 pb-3 text-lg font-semibold">{{ t("billing.webhooks.deliveries") }}</h2>
    <DataToolbar v-model:search="deliveriesTable.search.value" :active="deliveriesTable.active.value" @clear="deliveriesTable.clear()">
      <NativeSelect v-model="deliveriesTable.filters.value.state" class="w-40" :aria-label="t('billing.webhooks.status')">
        <option value="">{{ t("billing.webhooks.allStatuses") }}</option>
        <option v-for="s in deliveryStates" :key="s" :value="s">{{ stateWord(s) }}</option>
      </NativeSelect>
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.webhooks.event") }}</TableHead>
          <TableHead>{{ t("billing.webhooks.status") }}</TableHead>
          <TableHead>{{ t("billing.webhooks.attempts") }}</TableHead>
          <TableHead>{{ t("billing.webhooks.lastAnswer") }}</TableHead>
          <TableHead>{{ t("billing.webhooks.date") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.webhooks.actions") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="d in deliveriesTable.items.value" :key="d.id">
          <TableCell class="mono text-xs">{{ d.event_type }}</TableCell>
          <TableCell>
            <Badge :variant="stateVariant(d.state)">{{ stateWord(d.state) }}</Badge>
            <span v-if="d.state === 'WEBHOOK_DELIVERY_STATE_PENDING' && d.attempts" class="text-muted-foreground block text-xs">{{ t("billing.webhooks.nextTry", { when: when(d.next_attempt_time) }) }}</span>
          </TableCell>
          <TableCell class="tabular-nums">{{ d.attempts }}</TableCell>
          <TableCell class="text-xs">
            <template v-if="d.last_status">{{ d.last_status }} </template>{{ d.last_error }}
          </TableCell>
          <TableCell class="text-xs">{{ when(d.create_time) }}</TableCell>
          <TableCell class="text-end">
            <Button size="sm" variant="outline" :disabled="act.redeliver.isPending.value" @click="act.redeliver.mutate(d.id ?? '')">{{ t("billing.webhooks.redeliver") }}</Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!deliveries.isPending.value && log.length === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.webhooks.noDeliveries") }}</p>
    <p v-else-if="log.length > 0 && deliveriesTable.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="log.length > 0" v-model:page="deliveriesTable.page.value" v-model:size="deliveriesTable.size.value" :pages="deliveriesTable.pages.value" :total="deliveriesTable.total.value" />
  </Card>
</template>
