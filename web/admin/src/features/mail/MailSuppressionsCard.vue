<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { useRouteTenantId } from "@/shared/lib/routeTenant";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, type Tone } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useMailSuppressions, useRemoveMailSuppression } from "./api";

/**
 * The addresses the tenant's mail is not sent to: a permanent bounce, a complaint, or the operator's word. The seller sees why each is there and can mail one again
 * (a person who fixed their mailbox); if it bounces for good again it comes back on its own. The list is the notifier's, so it says so when the notifier is not
 * connected on this deployment (the error is the server's, in the reader's language).
 */
const { t } = useI18n();
const fmt = useFormat();
const tenantId = useRouteTenantId();
const canWrite = useCanWrite();
const list = useMailSuppressions(tenantId);
const remove = useRemoveMailSuppression(tenantId);
const rows = computed(() => list.data.value?.pages.flatMap((p) => p.suppressions) ?? []);
const TONES: Record<string, Tone> = { bounce: "warning", complaint: "danger", manual: "neutral" };

async function mailAgain(address: string) {
  if (!(await askConfirm({ body: t("billing.mail.suppressions.confirm", { address }), confirmLabel: t("billing.mail.suppressions.remove") }))) return;
  remove.mutate(address);
}
</script>

<template>
  <Card class="gap-0 p-3" data-testid="mail-suppressions">
    <h2 class="m-0 px-3 pt-2 text-base font-semibold">{{ t("billing.mail.suppressions.title") }}</h2>
    <p class="text-muted-foreground px-3 pt-1 pb-3 text-sm">{{ t("billing.mail.suppressions.hint") }}</p>
    <Skeleton v-if="list.isPending.value" class="mx-3 h-8 w-64" aria-busy="true" />
    <ErrorText :error="list.error.value ?? remove.error.value" class="px-3" />
    <p v-if="list.isSuccess.value && rows.length === 0" class="text-muted-foreground px-3 pb-2 text-sm" data-testid="mail-suppressions-empty">{{ t("billing.mail.suppressions.empty") }}</p>
    <Table v-if="rows.length > 0">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.mail.suppressions.columns.address") }}</TableHead>
          <TableHead>{{ t("billing.mail.suppressions.columns.reason") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("billing.mail.suppressions.columns.since") }}</TableHead>
          <TableHead v-if="canWrite" class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="s in rows" :key="s.address" :data-testid="`suppression-${s.address}`">
          <TableCell class="mono break-all">{{ s.address }}</TableCell>
          <TableCell>
            <StatusPill :tone="TONES[s.reason] ?? 'neutral'">{{ t(`billing.mail.suppressions.reasons.${s.reason}`) }}</StatusPill>
            <span v-if="s.detail" class="text-muted-foreground ms-2 text-xs break-words" dir="auto">{{ s.detail }}</span>
          </TableCell>
          <TableCell class="whitespace-nowrap max-md:hidden">{{ fmt.date(s.create_time, "day", "—") }}</TableCell>
          <TableCell v-if="canWrite">
            <Button type="button" size="sm" variant="outline" :disabled="remove.isPending.value" :data-testid="`suppression-remove-${s.address}`" @click="mailAgain(s.address)">{{ t("billing.mail.suppressions.remove") }}</Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <div v-if="list.hasNextPage.value" class="flex justify-center p-3">
      <Button type="button" variant="outline" :disabled="list.isFetchingNextPage.value" data-testid="suppressions-more" @click="list.fetchNextPage()">{{ t("billing.mail.loadMore") }}</Button>
    </div>
  </Card>
</template>
