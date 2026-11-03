<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { Textarea } from "@/shared/ui/textarea";
import { STATUS_CATEGORIES, useCancelScheduledTenantStatus, useScheduleTenantStatus, useTenant, useTenantLifecycle, type StatusCategory } from "./api";
import { useCanWrite } from "@/shared/lib/access";

const canWrite = useCanWrite();

/**
 * Retention and the schedule of a live tenant (plan 14), for the operator: the day a banned tenant's data is kept until (a job marks it due and deletes nothing), and the
 * suspension or freeze scheduled for it, with the form that schedules one for an active tenant and the button that cancels it.
 */
const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const tenant = useTenant(() => props.tenantId);
const live = computed(() => tenant.data.value?.environment === "ENVIRONMENT_LIVE");
const active = computed(() => tenant.data.value?.status === "TENANT_STATUS_ACTIVE");
const banned = computed(() => tenant.data.value?.status === "TENANT_STATUS_BANNED");
const lifecycle = useTenantLifecycle(() => props.tenantId, live);
const schedule = useScheduleTenantStatus();
const cancel = useCancelScheduledTenantStatus();
const l = computed(() => lifecycle.data.value);

const status = ref<"suspended" | "frozen">("suspended");
const when = ref("");
const category = ref<StatusCategory | "">("");
const reason = ref("");
/** The first moment that can be scheduled: the next minute, in the reader's clock (the input takes local time). */
const minTime = computed(() => new Date(Date.now() + 60_000 - new Date().getTimezoneOffset() * 60_000).toISOString().slice(0, 16));

function submit() {
  if (!when.value) return;
  schedule.mutate(
    { id: props.tenantId, status: status.value, time: new Date(when.value).toISOString(), category: category.value || undefined, reason: reason.value.trim() },
    { onSuccess: () => ((when.value = ""), (reason.value = ""), (category.value = "")) },
  );
}
async function drop() {
  if (await askConfirm({ body: t("tenant.lifecycle.confirmCancel"), destructive: true })) cancel.mutate(props.tenantId);
}
</script>

<template>
  <Card v-if="live" class="p-6" data-testid="tenant-lifecycle">
    <h2 class="m-0 text-lg font-semibold">{{ t("tenant.lifecycle.title") }}</h2>
    <Skeleton v-if="lifecycle.isPending.value" class="h-5 w-48" />
    <template v-else>
      <p v-if="l?.retention_due_time" class="m-0 text-sm" data-testid="lifecycle-retention-due">{{ t("tenant.lifecycle.retentionDue", { date: fmt.date(l.retention_due_time, "day") }) }}</p>
      <p v-else-if="l?.retention_until" class="m-0 text-sm" data-testid="lifecycle-retention">{{ t("tenant.lifecycle.retentionUntil", { date: fmt.date(l.retention_until, "day") }) }}</p>
      <p v-else-if="banned" class="text-muted-foreground m-0 text-sm">{{ t("tenant.lifecycle.retentionNone") }}</p>

      <div class="flex flex-wrap items-center justify-between gap-3 text-sm" data-testid="lifecycle-schedule">
        <span v-if="l?.scheduled_status">{{ t("tenant.lifecycle.scheduled", { status: t(`tenant.status.names.${l.scheduled_status}`), date: fmt.date(l.scheduled_time, "dateTime"), by: l.scheduled_by }) }}</span>
        <span v-else class="text-muted-foreground">{{ t("tenant.lifecycle.none") }}</span>
        <Button v-if="l?.scheduled_status" type="button" size="sm" variant="outline" :disabled="!canWrite || cancel.isPending.value" data-testid="lifecycle-cancel" @click="drop">{{ t("tenant.lifecycle.cancel") }}</Button>
      </div>
      <ErrorText :error="cancel.error.value" />

      <form v-if="active" class="grid gap-4 sm:grid-cols-2" @submit.prevent="submit">
        <p class="text-muted-foreground m-0 text-sm sm:col-span-2">{{ t("tenant.lifecycle.hint") }}</p>
        <FormField :label="t('tenant.lifecycle.status')" for="schedule-status">
          <NativeSelect id="schedule-status" v-model="status" data-testid="schedule-status">
            <option value="suspended">{{ t("tenant.status.names.suspended") }}</option>
            <option value="frozen">{{ t("tenant.status.names.frozen") }}</option>
          </NativeSelect>
        </FormField>
        <FormField :label="t('tenant.lifecycle.time')" for="schedule-time">
          <Input id="schedule-time" v-model="when" type="datetime-local" :min="minTime" data-testid="schedule-time" />
        </FormField>
        <FormField :label="t('tenant.lifecycle.category')" for="schedule-category">
          <NativeSelect id="schedule-category" v-model="category" data-testid="schedule-category">
            <option value="">{{ t("tenant.status.categoryNone") }}</option>
            <option v-for="c in STATUS_CATEGORIES" :key="c" :value="c">{{ t(`tenant.status.categories.${c}`) }}</option>
          </NativeSelect>
        </FormField>
        <FormField :label="t('tenant.lifecycle.reason')" for="schedule-reason">
          <Textarea id="schedule-reason" v-model="reason" maxlength="500" data-testid="schedule-reason" />
        </FormField>
        <ErrorText class="sm:col-span-2" :error="schedule.error.value" />
        <div class="sm:col-span-2">
          <Button type="submit" :disabled="!canWrite || !when || schedule.isPending.value" data-testid="schedule-submit">{{ t("tenant.lifecycle.submit") }}</Button>
        </div>
      </form>
    </template>
  </Card>
</template>
