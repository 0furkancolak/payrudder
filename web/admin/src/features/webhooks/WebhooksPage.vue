<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { EVENT_TYPES, useWebhookActions, useWebhookDeliveries, useWebhookEndpoints } from "./api";

const { t, locale } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const endpoints = useWebhookEndpoints(tenantId);
const act = useWebhookActions(tenantId);

const url = ref("");
const description = ref("");
const types = ref<string[]>([]);
const selected = ref("");
const deliveries = useWebhookDeliveries(tenantId, selected);
const list = computed(() => endpoints.data.value?.endpoints ?? []);
const log = computed(() => deliveries.data.value?.deliveries ?? []);
/** A secret is shown right after it is made and never again. */
const secret = computed(() => ((act.create.data.value ?? act.rotate.data.value) as { secret?: string } | undefined)?.secret ?? "");
const when = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "short", timeStyle: "medium" }).format(new Date(v)) : "—");
const stateVariant = (s?: string) => (s === "WEBHOOK_DELIVERY_STATE_DELIVERED" ? "success" : s === "WEBHOOK_DELIVERY_STATE_FAILED" ? "destructive" : "secondary");
const stateWord = (s?: string) => t(`billing.webhooks.state.${(s ?? "").replace("WEBHOOK_DELIVERY_STATE_", "").toLowerCase()}`);
const failure = computed(() => {
  for (const m of Object.values(act)) if (m.error.value) return errorText(m.error.value);
  return "";
});

function create() {
  act.create.mutate({ url: url.value.trim(), description: description.value.trim(), event_types: [...types.value] }, { onSuccess: () => (url.value = description.value = "") });
}
function remove(id: string | undefined) {
  if (id && window.confirm(t("billing.webhooks.confirmDelete"))) act.remove.mutate(id);
}
function toggle(e: { id?: string; url?: string; description?: string; event_types?: string[]; active?: boolean }) {
  act.update.mutate({ id: e.id ?? "", url: e.url ?? "", description: e.description ?? "", event_types: e.event_types ?? [], active: !e.active });
}
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.webhooks.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.webhooks.hint") }}</p>
    <form class="grid gap-3" role="form" :aria-label="t('billing.webhooks.formLabel')" @submit.prevent="create">
      <div class="grid gap-3 sm:grid-cols-2">
        <Input v-model="url" type="url" required :aria-label="t('billing.webhooks.url')" placeholder="https://example.com/quotify-webhook" />
        <Input v-model="description" maxlength="200" :aria-label="t('billing.webhooks.description')" :placeholder="t('billing.webhooks.description')" />
      </div>
      <fieldset class="grid gap-1 sm:grid-cols-2 lg:grid-cols-3">
        <legend class="text-muted-foreground mb-1 text-sm">{{ t("billing.webhooks.events") }}</legend>
        <label v-for="type in EVENT_TYPES" :key="type" class="mono flex items-center gap-2 text-xs">
          <input v-model="types" type="checkbox" :value="type" class="size-4" />{{ type }}
        </label>
      </fieldset>
      <p class="text-muted-foreground text-xs">{{ t("billing.webhooks.eventsHint") }}</p>
      <div><Button type="submit" :disabled="act.create.isPending.value">{{ t("billing.webhooks.add") }}</Button></div>
    </form>
    <div v-if="secret" class="border-warning/50 bg-warning/10 rounded-md border p-3" role="status">
      <p class="text-sm font-medium">{{ t("billing.webhooks.secretOnce") }}</p>
      <p class="mono break-all text-sm" data-testid="webhook-secret">{{ secret }}</p>
    </div>
    <p v-if="failure" class="text-destructive text-sm" role="alert">{{ failure }}</p>
    <p v-if="endpoints.error.value" class="text-destructive text-sm" role="alert">{{ errorText(endpoints.error.value) }}</p>
  </Card>

  <Card class="mb-6 p-3">
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
        <TableRow v-for="e in list" :key="e.id">
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
  </Card>

  <Card v-if="selected" class="p-3" role="region" :aria-label="t('billing.webhooks.deliveries')">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.webhooks.deliveries") }}</h2>
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
        <TableRow v-for="d in log" :key="d.id">
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
  </Card>
</template>
