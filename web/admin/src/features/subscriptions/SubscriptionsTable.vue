<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { Subscription } from "@/shared/api/client";
import { useFormat } from "@/shared/lib/format";
import { enumWord, label } from "@/shared/lib/labels";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useSubscriptions } from "./api";
import { intervalKey } from "./lib";

/** The tenant's subscriptions; "Manage" selects one for the card below. */
const props = defineProps<{ tenantId: string; selectedId: string }>();
const emit = defineEmits<{ select: [id: string] }>();
const { t, te } = useI18n();
const fmt = useFormat();
const subs = useSubscriptions(() => props.tenantId);
const list = computed(() => subs.data.value?.subscriptions ?? []);

const state = (s: Subscription) => enumWord(s.state, "SUBSCRIPTION_STATE_");
const BAD_STATES = ["cancelled", "past_due"];
const stateVariant = (s: Subscription) => (state(s) === "active" ? "success" : BAD_STATES.includes(state(s)) ? "destructive" : "secondary");
const date = (v?: string | null) => fmt.date(v, "day", "—");
</script>

<template>
  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.tabs.subscriptions") }}</h2>
    <ErrorText :error="subs.error.value" class="px-3" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.subscriptions.customer") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.package") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.interval") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.currency") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.provider") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.since") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.until") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="s in list" :key="s.id" :data-state="s.id === selectedId ? 'selected' : undefined">
          <TableCell class="font-medium">{{ s.customer_external_id }}</TableCell>
          <TableCell class="mono">{{ s.plan_code }} v{{ s.plan_version }}</TableCell>
          <TableCell>
            <Badge :variant="stateVariant(s)">{{ label(t, te, "subscriptionState", state(s)) }}</Badge>
            <span v-if="s.cancel_at_period_end" class="text-muted-foreground ms-2 text-xs">{{ t("billing.subscriptions.endsWithPeriod") }}</span>
          </TableCell>
          <TableCell>{{ t(`billing.subscriptions.${intervalKey(s.interval)}`) }}</TableCell>
          <TableCell>{{ s.currency }}</TableCell>
          <TableCell>
            {{ s.provider || "—" }}
            <Badge v-if="s.owner === 'provider'" variant="secondary" class="ms-1">{{ t("billing.subscriptions.runByProvider") }}</Badge>
          </TableCell>
          <TableCell>{{ date(s.start_time) }}</TableCell>
          <TableCell>{{ date(s.end_time) }}</TableCell>
          <TableCell class="text-end"><Button size="sm" variant="outline" @click="emit('select', s.id ?? '')">{{ t("billing.subscriptions.manage") }}</Button></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!subs.isPending.value && list.length === 0 && !subs.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.subscriptions.empty") }}
    </p>
  </Card>
</template>
