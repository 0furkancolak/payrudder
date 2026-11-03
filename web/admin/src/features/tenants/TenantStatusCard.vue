<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { askConfirm } from "@/shared/lib/confirm";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { FormField } from "@/shared/ui/form-field";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, type Tone } from "@/shared/ui/status-pill";
import { Textarea } from "@/shared/ui/textarea";
import { STATUS_CATEGORIES, useSetTenantStatus, useTenant, useTenantStatusEvents, type StatusCategory, type TenantStatusWord } from "./api";
import { useCanWrite } from "@/shared/lib/access";

const canWrite = useCanWrite();

/**
 * The operator's hand on a live tenant: where it is (active, suspended, frozen, banned), the form that moves it with a category and a reason, and the history of
 * every change (who, when, why). A ban, and lifting one, needs a reason; where a ban needs a second operator the first request is shown as waiting. A sandbox
 * tenant is always active and an inactive live one is activated from its go-live checklist, so those get no form.
 */
const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const tenant = useTenant(() => props.tenantId);
const events = useTenantStatusEvents(() => props.tenantId);
const set = useSetTenantStatus();

const WORDS = { TENANT_STATUS_INACTIVE: "inactive", TENANT_STATUS_ACTIVE: "active", TENANT_STATUS_SUSPENDED: "suspended", TENANT_STATUS_FROZEN: "frozen", TENANT_STATUS_BANNED: "banned" } as const;
type Word = (typeof WORDS)[keyof typeof WORDS];
const wordOf = (s?: string): Word | "" => (s && s in WORDS ? WORDS[s as keyof typeof WORDS] : "");
const TONES: Record<Word, Tone> = { active: "success", inactive: "neutral", suspended: "danger", frozen: "warning", banned: "danger" };

const current = computed(() => wordOf(tenant.data.value?.status));
const live = computed(() => tenant.data.value?.environment === "ENVIRONMENT_LIVE");
/** Only a live tenant that is in service (or was put out of it) can be moved here. */
const editable = computed(() => live.value && current.value !== "" && current.value !== "inactive");
const targets = computed<TenantStatusWord[]>(() => (["active", "suspended", "frozen", "banned"] as const).filter((w) => w !== current.value));

/**
 * The history filters. The server sends the latest 100 changes, so they narrow that list in the browser, and apply on their button like every filter of the console.
 * The status is where the tenant went; the kind separates changes from bans still waiting for a second operator.
 */
const toFilter = ref<string[]>([]);
const categoryFilter = ref<string[]>([]);
const kindFilter = ref<string[]>([]);
const allEvents = computed(() => events.data.value?.events ?? []);
const shown = computed(() =>
  allEvents.value.filter(
    (e) =>
      (toFilter.value.length === 0 || toFilter.value.includes(wordOf(e.to_status))) &&
      (categoryFilter.value.length === 0 || categoryFilter.value.includes(e.category ?? "")) &&
      (kindFilter.value.length === 0 || kindFilter.value.includes(e.kind ?? "")),
  ),
);
const filtered = computed(() => toFilter.value.length + categoryFilter.value.length + kindFilter.value.length > 0);
const toOptions = computed(() => (["active", "suspended", "frozen", "banned"] as const).map((w) => ({ value: w, label: t(`tenant.status.names.${w}`) })));
const categoryOptions = computed(() => STATUS_CATEGORIES.map((c) => ({ value: c, label: t(`tenant.status.categories.${c}`) })));
const kindOptions = computed(() => [
  { value: "changed", label: t("tenant.status.kinds.changed") },
  { value: "requested", label: t("tenant.status.kinds.requested") },
]);

const to = ref<TenantStatusWord | "">("");
const category = ref<StatusCategory | "">("");
const reason = ref("");
/** Nothing is chosen until the operator chooses: a status that is already selected is one slip away from a ban. */
const chosen = computed<TenantStatusWord | "">(() => (to.value && targets.value.includes(to.value) ? to.value : ""));
const needsReason = computed(() => chosen.value === "banned" || current.value === "banned");
const missingReason = computed(() => needsReason.value && reason.value.trim() === "");

async function apply() {
  const status = chosen.value;
  if (!status || missingReason.value) return;
  const body = t(`tenant.status.confirm.${status}`, { name: tenant.data.value?.name ?? "" });
  if (!(await askConfirm({ body, destructive: status !== "active" }))) return;
  set.mutate(
    { id: props.tenantId, status, reason: reason.value.trim(), category: category.value || undefined },
    { onSuccess: () => ((reason.value = ""), (category.value = ""), (to.value = "")) },
  );
}
</script>

<template>
  <Card class="p-6" data-testid="tenant-status">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="m-0 text-lg font-semibold">{{ t("tenant.status.title") }}</h2>
      <Skeleton v-if="tenant.isPending.value" class="h-6 w-24 rounded-full" />
      <StatusPill v-else-if="current" :tone="TONES[current]" data-testid="tenant-status-current">{{ t(`tenant.status.names.${current}`) }}</StatusPill>
    </div>
    <p v-if="current" class="text-muted-foreground m-0 text-sm">{{ t(`tenant.status.effects.${current}`) }}</p>

    <form v-if="editable" class="grid gap-4 sm:grid-cols-2" @submit.prevent="apply">
      <FormField :label="t('tenant.status.to')" for="status-to">
        <NativeSelect id="status-to" v-model="to" data-testid="status-to">
          <option value="" disabled>{{ t("tenant.status.choose") }}</option>
          <option v-for="w in targets" :key="w" :value="w">{{ t(`tenant.status.names.${w}`) }}</option>
        </NativeSelect>
      </FormField>
      <FormField :label="t('tenant.status.category')" for="status-category">
        <NativeSelect id="status-category" v-model="category" data-testid="status-category">
          <option value="">{{ t("tenant.status.categoryNone") }}</option>
          <option v-for="c in STATUS_CATEGORIES" :key="c" :value="c">{{ t(`tenant.status.categories.${c}`) }}</option>
        </NativeSelect>
      </FormField>
      <FormField class="sm:col-span-2" :label="t('tenant.status.reason')" for="status-reason" :hint="needsReason ? t('tenant.status.reasonRequired') : t('tenant.status.reasonHint')">
        <Textarea id="status-reason" v-model="reason" maxlength="500" :aria-invalid="missingReason || undefined" data-testid="status-reason" />
      </FormField>
      <p v-if="chosen" class="text-muted-foreground m-0 text-sm sm:col-span-2" data-testid="status-effect">{{ t(`tenant.status.effects.${chosen}`) }}</p>
      <ErrorText class="sm:col-span-2" :error="set.error.value" />
      <div class="sm:col-span-2">
        <Button type="submit" :disabled="!canWrite || !chosen || set.isPending.value || missingReason" data-testid="status-apply">{{ chosen ? t("tenant.status.apply", { status: t(`tenant.status.names.${chosen}`) }) : t("tenant.status.applyNone") }}</Button>
      </div>
    </form>
    <p v-else-if="current && !live" class="text-muted-foreground m-0 text-sm">{{ t("tenant.status.sandbox") }}</p>
    <p v-else-if="current === 'inactive'" class="text-muted-foreground m-0 text-sm">{{ t("tenant.status.inactive") }}</p>

    <section :aria-label="t('tenant.status.history')" data-testid="tenant-status-history">
      <h3 class="m-0 mb-2 text-sm font-semibold">{{ t("tenant.status.history") }}</h3>
      <Skeleton v-if="events.isPending.value" class="h-5 w-40" />
      <ErrorState v-else-if="events.isError.value" :error="events.error.value" :retrying="events.isFetching.value" @retry="events.refetch()" />
      <p v-else-if="allEvents.length === 0" class="text-muted-foreground m-0 text-sm">{{ t("tenant.status.historyEmpty") }}</p>
      <template v-else>
        <div class="mb-2 flex flex-wrap items-center gap-2" role="group" :aria-label="t('tenant.status.filters')" data-testid="tenant-status-filters">
          <FilterSelect v-model="toFilter" multiple :label="t('tenant.status.filterTo')" :options="toOptions" />
          <FilterSelect v-model="categoryFilter" multiple :label="t('tenant.status.category')" :options="categoryOptions" />
          <FilterSelect v-model="kindFilter" multiple :label="t('tenant.status.filterKind')" :options="kindOptions" />
          <span v-if="filtered" class="text-muted-foreground text-xs" role="status" data-testid="tenant-status-count">{{ t("tenant.status.shown", { shown: shown.length, total: allEvents.length }) }}</span>
        </div>
        <p v-if="shown.length === 0" class="text-muted-foreground m-0 text-sm" data-testid="tenant-status-nomatch">{{ t("tenant.status.noMatch") }}</p>
      </template>
      <ul v-if="shown.length > 0" class="divide-border/70 m-0 flex list-none flex-col divide-y p-0 text-sm">
        <li v-for="e in shown" :key="e.id" class="flex flex-col gap-0.5 py-2" data-testid="tenant-status-event">
          <span class="font-medium">
            <template v-if="e.kind === 'requested'">{{ t("tenant.status.requested", { to: t(`tenant.status.names.${wordOf(e.to_status) || 'active'}`) }) }}</template>
            <template v-else>{{ t("tenant.status.changed", { from: t(`tenant.status.names.${wordOf(e.from_status) || 'active'}`), to: t(`tenant.status.names.${wordOf(e.to_status) || 'active'}`) }) }}</template>
            <template v-if="e.category"> · {{ t(`tenant.status.categories.${e.category}`) }}</template>
          </span>
          <span v-if="e.reason" class="text-muted-foreground break-words" dir="auto">{{ e.reason }}</span>
          <span class="text-muted-foreground text-xs">{{ fmt.date(e.create_time, "dateTime") }} · {{ t("tenant.status.by", { actor: e.actor }) }}<template v-if="e.approved_by"> · {{ t("tenant.status.approvedBy", { actor: e.approved_by }) }}</template></span>
        </li>
      </ul>
    </section>
  </Card>
</template>
