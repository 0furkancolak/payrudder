<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { FormField } from "@/shared/ui/form-field";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, type Tone } from "@/shared/ui/status-pill";
import { Textarea } from "@/shared/ui/textarea";
import { useDecideTenantAppeal, useTenantAppeals, type TenantAppeal } from "./api";

/**
 * The owners' appeals against a hold (plan 14), for the operator: one tenant's (on its page), or every tenant's open ones (`all`, on the list of tenants). A row opens the
 * sheet where the operator reads the statement and accepts (the tenant is reinstated) or rejects (an answer is required); both mail the owners the answer.
 */
const props = defineProps<{ tenantId?: string; all?: boolean }>();
const { t } = useI18n();
const fmt = useFormat();
const statusFilter = ref<string[]>([]);
const filter = computed(() => ({ tenantId: props.tenantId, status: props.all ? ("open" as const) : undefined }));
const list = useTenantAppeals(filter);
const decide = useDecideTenantAppeal();
const rows = computed(() => (list.data.value?.appeals ?? []).filter((a) => statusFilter.value.length === 0 || statusFilter.value.includes(a.status)));
const appName = (a: TenantAppeal) => list.data.value?.app_names?.[a.tenant_id] ?? a.tenant_id;
const TONES: Record<string, Tone> = { open: "warning", accepted: "success", rejected: "danger" };
const statusOptions = computed(() => (["open", "accepted", "rejected"] as const).map((s) => ({ value: s, label: t(`tenant.appeals.status.${s}`) })));

const current = ref<TenantAppeal | null>(null);
const answer = ref("");
const open = computed({ get: () => current.value !== null, set: (v) => !v && (current.value = null) });
function openSheet(a: TenantAppeal) {
  decide.reset();
  answer.value = "";
  current.value = a;
}
async function send(decision: "accepted" | "rejected") {
  const a = current.value;
  if (!a) return;
  if (decision === "accepted" && !(await askConfirm({ body: t("tenant.appeals.confirmAccept", { app: appName(a) }) }))) return;
  decide.mutate({ id: a.id, decision, answer: answer.value.trim() }, { onSuccess: () => (current.value = null) });
}
</script>

<template>
  <Card v-if="!all || rows.length > 0 || list.isPending.value || list.error.value" class="p-6" data-testid="tenant-appeals">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="m-0 text-lg font-semibold">{{ all ? t("tenant.appeals.openTitle") : t("tenant.appeals.title") }}</h2>
      <FilterSelect v-if="!all" v-model="statusFilter" multiple :label="t('tenant.appeals.columns.status')" :options="statusOptions" />
    </div>
    <Skeleton v-if="list.isPending.value" class="h-5 w-48" />
    <ErrorState :error="list.error.value" compact :retrying="list.isFetching.value" @retry="list.refetch()" />
    <p v-if="!list.isPending.value && !list.error.value && rows.length === 0" class="text-muted-foreground m-0 text-sm" data-testid="appeals-empty">{{ t("tenant.appeals.empty") }}</p>
    <ul v-else-if="rows.length > 0" class="divide-border/70 m-0 flex list-none flex-col divide-y p-0 text-sm">
      <li v-for="a in rows" :key="a.id" class="flex flex-wrap items-start justify-between gap-3 py-3" :data-testid="`appeal-${a.id}`">
        <div class="flex min-w-0 flex-1 basis-72 flex-col gap-1">
          <span class="flex flex-wrap items-center gap-2 font-medium">
            <StatusPill :tone="TONES[a.status] ?? 'neutral'">{{ t(`tenant.appeals.status.${a.status}`) }}</StatusPill>
            <Link v-if="all" :to="`/tenants/${a.tenant_id}`" class="underline underline-offset-4" :data-testid="`appeal-tenant-${a.id}`">{{ appName(a) }}</Link>
            <span class="text-muted-foreground font-normal">{{ t("tenant.appeals.columns.held") }} {{ t(`tenant.status.names.${a.held_status}`) }}</span>
          </span>
          <span class="break-words" dir="auto">{{ a.statement }}</span>
          <span v-if="a.answer" class="text-muted-foreground break-words" dir="auto">{{ a.answer }}</span>
          <span class="text-muted-foreground text-xs">{{ a.submitted_by }} · {{ fmt.date(a.create_time, "dateTime") }}<template v-if="a.decided_by"> · {{ a.decided_by }}</template></span>
        </div>
        <Button v-if="a.status === 'open'" type="button" size="sm" variant="outline" :data-testid="`appeal-decide-${a.id}`" @click="openSheet(a)">{{ t("tenant.appeals.decide") }}</Button>
      </li>
    </ul>
  </Card>

  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("tenant.appeals.sheetTitle", { app: current ? appName(current) : "" }) }}</SheetTitle>
        <SheetDescription dir="auto">{{ current?.statement }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="appeal-decision" class="grid gap-4" @submit.prevent>
          <FormField :label="t('tenant.appeals.answer')" for="appeal-answer" :hint="t('tenant.appeals.answerHint')">
            <Textarea id="appeal-answer" v-model="answer" maxlength="1000" data-testid="appeal-answer" />
          </FormField>
          <ErrorText :error="decide.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="outline" :disabled="decide.isPending.value || answer.trim() === ''" data-testid="appeal-reject" @click="send('rejected')">{{ t("tenant.appeals.reject") }}</Button>
        <Button type="button" :disabled="decide.isPending.value" data-testid="appeal-accept" @click="send('accepted')">{{ t("tenant.appeals.accept") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
