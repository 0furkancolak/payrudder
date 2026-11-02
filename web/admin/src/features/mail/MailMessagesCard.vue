<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { useRouteTenantId } from "@/shared/lib/routeTenant";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, type Tone } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useMailMessages, type MailMessage } from "./api";

/**
 * The tenant's recent mail, newest first: when it was claimed, to whom, which mail, and what became of it (sending, sent, and what the provider reported). It is the
 * notifier's own record, a page at a time; mail Payrudder sends for itself is not in it.
 */
const { t } = useI18n();
const fmt = useFormat();
const tenantId = useRouteTenantId();
const list = useMailMessages(tenantId);
const rows = computed(() => list.data.value?.pages.flatMap((p) => p.messages) ?? []);
const connected = computed(() => list.data.value?.pages[0]?.connected !== false);
const TONES: Record<MailMessage["state"], Tone> = { sending: "info", sent: "neutral", delivered: "success", delayed: "warning", bounced: "danger", soft_bounced: "warning", complained: "danger" };
</script>

<template>
  <Card class="gap-0 p-3" data-testid="mail-messages">
    <h2 class="m-0 px-3 pt-2 text-base font-semibold">{{ t("billing.mail.messages.title") }}</h2>
    <p class="text-muted-foreground px-3 pt-1 pb-3 text-sm">{{ t("billing.mail.messages.hint") }}</p>
    <Skeleton v-if="list.isPending.value" class="mx-3 h-8 w-64" aria-busy="true" />
    <ErrorText :error="list.error.value" class="px-3" />
    <p v-if="list.isSuccess.value && !connected" class="text-muted-foreground px-3 pb-2 text-sm" data-testid="mail-messages-not-connected">{{ t("billing.mail.notConnected") }}</p>
    <p v-else-if="list.isSuccess.value && rows.length === 0" class="text-muted-foreground px-3 pb-2 text-sm" data-testid="mail-messages-empty">{{ t("billing.mail.messages.empty") }}</p>
    <Table v-if="rows.length > 0">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.mail.messages.columns.when") }}</TableHead>
          <TableHead>{{ t("billing.mail.messages.columns.to") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("billing.mail.messages.columns.mail") }}</TableHead>
          <TableHead>{{ t("billing.mail.messages.columns.state") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="m in rows" :key="m.key" :data-testid="`message-${m.key}`">
          <TableCell class="whitespace-nowrap">{{ fmt.date(m.claim_time, "dateTime", "—") }}</TableCell>
          <TableCell class="mono break-all">{{ m.recipient }}</TableCell>
          <TableCell class="mono text-xs max-md:hidden">{{ m.template }}</TableCell>
          <TableCell><StatusPill :tone="TONES[m.state] ?? 'neutral'">{{ t(`billing.mail.messages.states.${m.state}`) }}</StatusPill></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <div v-if="list.hasNextPage.value" class="flex justify-center p-3">
      <Button type="button" variant="outline" :disabled="list.isFetchingNextPage.value" data-testid="messages-more" @click="list.fetchNextPage()">{{ t("billing.mail.loadMore") }}</Button>
    </div>
  </Card>
</template>
