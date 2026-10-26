<script setup lang="ts">
import { refDebounced } from "@vueuse/core";
import { Check, Minus } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useAffiliates, useBalances, useUpdateAffiliate } from "./api";
import AddAffiliateDialog from "./AddAffiliateDialog.vue";
import EditAffiliateDialog from "./EditAffiliateDialog.vue";
import LinkDialog from "./LinkDialog.vue";
import { affiliateTone, balancesByAffiliate, type Affiliate } from "./logic";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { usePager } from "./paging";
import ReferralsDialog from "./ReferralsDialog.vue";

const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
const tenantId = computed(() => props.tenantId);

const status = ref("");
const search = ref("");
const searchNow = refDebounced(search, 300);
const pager = usePager();
watch([status, searchNow], () => pager.reset());

const list = useAffiliates(tenantId, () => ({ status: status.value, search: searchNow.value }), pager.token, pager.size);
const waiting = useAffiliates(tenantId, () => ({ status: "pending", search: "" }), "");
const balances = useBalances(tenantId);
const byPartner = computed(() => balancesByAffiliate(balances.data.value));
const update = useUpdateAffiliate(tenantId);

const addOpen = ref(false);
const dialog = ref<"" | "edit" | "link" | "referrals">("");
const selected = ref<Affiliate | null>(null);
function open(kind: "edit" | "link" | "referrals", a: Affiliate) {
  selected.value = a;
  dialog.value = kind;
}
const dialogOpen = (kind: string) => computed({ get: () => dialog.value === kind, set: (v: boolean) => (dialog.value = v ? (kind as "edit") : "") });
const editOpen = dialogOpen("edit");
const linkOpen = dialogOpen("link");
const referralsOpen = dialogOpen("referrals");

function setStatus(a: Affiliate, next: "active" | "suspended") {
  update.mutate({ id: a.id ?? "", body: { status: next } });
}
</script>

<template>
  <div v-if="(waiting.data.value?.total ?? 0) > 0" data-testid="pending-banner" role="status" class="bg-brand-soft text-brand-strong rounded-2xl px-4 py-3 text-sm">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <span>{{ t("affiliates.partners.waiting", { count: waiting.data.value?.total ?? 0 }) }}</span>
      <Button v-if="status !== 'pending'" type="button" size="sm" variant="outline" @click="status = 'pending'">{{ t("affiliates.partners.showWaiting") }}</Button>
    </div>
  </div>

  <Card class="p-3">
    <DataToolbar v-model:search="search" :placeholder="t('affiliates.partners.search')" :active="status !== '' || search !== ''" class="pt-2" @clear="status = ''; search = ''">
      <NativeSelect v-model="status" class="w-44" :aria-label="t('affiliates.common.status')">
        <option value="">{{ t("affiliates.partners.allStatuses") }}</option>
        <option v-for="s in ['pending', 'active', 'suspended']" :key="s" :value="s">{{ t(`affiliates.status.affiliate.${s}`) }}</option>
      </NativeSelect>
      <template #actions>
        <Button type="button" :disabled="!canWrite" @click="addOpen = true">{{ t("affiliates.partners.add") }}</Button>
      </template>
    </DataToolbar>
    <ErrorText :error="list.error.value" class="px-3" />
    <ErrorText :error="update.error.value" class="px-3" />
    <Skeleton v-if="list.isPending.value" class="mx-3 h-32" />
    <p v-else-if="list.isSuccess.value && !list.data.value?.items.length" class="text-muted-foreground px-3 py-4 text-sm">{{ t("affiliates.partners.empty") }}</p>
    <Table v-else-if="list.data.value">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("affiliates.partners.code") }}</TableHead>
          <TableHead>{{ t("affiliates.partners.partner") }}</TableHead>
          <TableHead>{{ t("affiliates.common.status") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.partners.clicks") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.partners.referrals") }}</TableHead>
          <TableHead>{{ t("affiliates.partners.signedIn") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.partners.balance") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="a in list.data.value.items" :key="a.id" :data-affiliate="a.code">
          <TableCell class="mono font-medium">{{ a.code }}</TableCell>
          <TableCell class="min-w-0">
            <div class="font-medium">{{ a.name || a.email }}</div>
            <div v-if="a.name" class="text-muted-foreground text-xs" dir="auto">{{ a.email }}</div>
            <div v-if="a.source === 'application'" class="text-muted-foreground text-xs">{{ t("affiliates.partners.applied") }}</div>
          </TableCell>
          <TableCell><StatusPill :tone="affiliateTone(a.status)">{{ t(`affiliates.status.affiliate.${a.status}`) }}</StatusPill></TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.count(Number(a.clicks ?? 0)) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.count(Number(a.referrals ?? 0)) }}</TableCell>
          <TableCell>
            <Check v-if="a.signed_in" class="text-success size-4" aria-hidden="true" /><Minus v-else class="text-muted-foreground size-4" aria-hidden="true" />
            <span class="sr-only">{{ a.signed_in ? t("affiliates.common.yes") : t("affiliates.common.no") }}</span>
          </TableCell>
          <TableCell class="text-end text-xs tabular-nums">
            <template v-if="byPartner.get(a.id ?? '')?.length">
              <div v-for="b in byPartner.get(a.id ?? '')" :key="b.currency">
                {{ t("affiliates.partners.available", { amount: fmt.amount(b.approved_minor, b.currency) }) }}
                <div class="text-muted-foreground">{{ t("affiliates.partners.paidAmount", { amount: fmt.amount(b.paid_minor, b.currency) }) }}</div>
              </div>
            </template>
            <span v-else class="text-muted-foreground">—</span>
          </TableCell>
          <TableCell class="text-end">
            <div class="flex flex-wrap justify-end gap-1.5">
              <Button v-if="a.status === 'pending'" type="button" size="sm" :disabled="!canWrite || update.isPending.value" @click="setStatus(a, 'active')">{{ t("affiliates.partners.approve") }}</Button>
              <Button v-if="a.status === 'active'" type="button" size="sm" variant="outline" :disabled="!canWrite || update.isPending.value" @click="setStatus(a, 'suspended')">{{ t("affiliates.partners.suspend") }}</Button>
              <Button v-if="a.status === 'suspended'" type="button" size="sm" variant="outline" :disabled="!canWrite || update.isPending.value" @click="setStatus(a, 'active')">{{ t("affiliates.partners.reinstate") }}</Button>
              <Button type="button" size="sm" variant="outline" :disabled="!canWrite" @click="open('edit', a)">{{ t("affiliates.partners.edit") }}</Button>
              <Button type="button" size="sm" variant="outline" @click="open('link', a)">{{ t("affiliates.partners.linkAndCode") }}</Button>
              <Button type="button" size="sm" variant="outline" @click="open('referrals', a)">{{ t("affiliates.partners.referralsOf") }}</Button>
            </div>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.data.value" v-model:size="pager.size.value" :page="pager.page.value" :has-prev="pager.hasPrev.value" :has-next="!!list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>

  <AddAffiliateDialog v-model:open="addOpen" :tenant-id="tenantId" />
  <EditAffiliateDialog v-model:open="editOpen" :tenant-id="tenantId" :affiliate="selected" />
  <LinkDialog v-model:open="linkOpen" :tenant-id="tenantId" :affiliate="selected" />
  <ReferralsDialog v-model:open="referralsOpen" :tenant-id="tenantId" :affiliate="selected" />
</template>
