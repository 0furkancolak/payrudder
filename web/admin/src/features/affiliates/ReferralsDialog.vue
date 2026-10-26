<script setup lang="ts">
import { computed, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useReferrals } from "./api";
import type { Affiliate } from "./logic";
import { DataPagination } from "@/shared/ui/data-table";
import { usePager } from "./paging";

/** The customers one partner brought. The e-mail address comes masked from the API (j***@example.com). */
const props = defineProps<{ tenantId: string; affiliate: Affiliate | null }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const fmt = useFormat();
const pager = usePager();
const id = computed(() => (open.value ? (props.affiliate?.id ?? "") : ""));
watch(id, () => pager.reset());
const referrals = useReferrals(() => props.tenantId, id, pager.token, pager.size);
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-3xl">
      <DialogHeader>
        <DialogTitle>{{ t("affiliates.referrals.title", { code: affiliate?.code ?? "" }) }}</DialogTitle>
        <DialogDescription>{{ t("affiliates.referrals.hint") }}</DialogDescription>
      </DialogHeader>
      <ErrorText :error="referrals.error.value" />
      <Skeleton v-if="referrals.isPending.value" class="h-24" />
      <p v-else-if="referrals.isSuccess.value && !referrals.data.value?.items.length" class="text-muted-foreground text-sm">{{ t("affiliates.referrals.empty") }}</p>
      <Table v-else-if="referrals.data.value">
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("affiliates.referrals.customer") }}</TableHead>
            <TableHead>{{ t("affiliates.referrals.email") }}</TableHead>
            <TableHead>{{ t("affiliates.referrals.source") }}</TableHead>
            <TableHead>{{ t("affiliates.referrals.time") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="r in referrals.data.value.items" :key="r.customer_external_id">
            <TableCell class="mono">{{ r.customer_external_id }}</TableCell>
            <TableCell dir="ltr" class="text-start">{{ r.customer_email_masked }}</TableCell>
            <TableCell>{{ t(`affiliates.referrals.sources.${r.source}`) }}</TableCell>
            <TableCell>{{ fmt.date(r.attribute_time, "dateTime") }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <DataPagination v-if="referrals.data.value" v-model:size="pager.size.value" :page="pager.page.value" :has-prev="pager.hasPrev.value" :has-next="!!referrals.data.value.next" :total="referrals.data.value.total" @prev="pager.prev()" @next="pager.next(referrals.data.value.next)" />
      <DialogFooter>
        <Button type="button" variant="outline" @click="open = false">{{ t("affiliates.common.close") }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
