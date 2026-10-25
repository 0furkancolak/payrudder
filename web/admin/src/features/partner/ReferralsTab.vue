<script setup lang="ts">
import { useI18n } from "vue-i18n";
import Pager from "@/features/affiliates/Pager.vue";
import { usePager } from "@/features/affiliates/paging";
import { useFormat } from "@/shared/lib/format";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useReferrals } from "./api";

/** The customers the partner brought. The e-mail address arrives masked (j***@example.com); nothing else about the customer is sent. */
const props = defineProps<{ id: string }>();
const { t } = useI18n();
const fmt = useFormat();
const pager = usePager();
const list = useReferrals(() => props.id, pager.token);
</script>

<template>
  <Card class="p-3" data-testid="referrals">
    <p class="text-muted-foreground m-0 px-3 pt-2 text-xs">{{ t("partner.referrals.hint") }}</p>
    <ErrorText :error="list.error.value" class="px-3" />
    <Skeleton v-if="list.isPending.value" class="mx-3 h-24" />
    <p v-else-if="list.isSuccess.value && !list.data.value?.items.length" class="text-muted-foreground m-0 px-3 py-4 text-sm">{{ t("partner.referrals.empty") }}</p>
    <Table v-else-if="list.data.value">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("partner.referrals.customer") }}</TableHead>
          <TableHead>{{ t("partner.referrals.source") }}</TableHead>
          <TableHead>{{ t("partner.referrals.time") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(r, i) in list.data.value.items" :key="i">
          <TableCell dir="ltr" class="text-start" data-cell="email">{{ r.customer_email_masked }}</TableCell>
          <TableCell>{{ t(`affiliates.referrals.sources.${r.source}`) }}</TableCell>
          <TableCell>{{ fmt.date(r.attribute_time, "dateTime") }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <Pager v-if="list.data.value" :has-prev="pager.hasPrev.value" :next="list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>
</template>
