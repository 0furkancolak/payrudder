<script setup lang="ts">
import { CalendarClock } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { useAppHold } from "./api";

/** "{app} will be suspended on {date}": a suspension or a freeze the operator scheduled for an app that is still in service (plan 14), told to its people ahead of time. */
const props = defineProps<{ id: string; name: string }>();
const { t } = useI18n();
const fmt = useFormat();
const hold = useAppHold(() => props.id);
const status = computed(() => hold.data.value?.scheduled_status ?? "");
</script>

<template>
  <p v-if="status" class="bg-warning-soft text-warning mt-0 mb-6 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="hold-scheduled">
    <CalendarClock class="size-4 shrink-0" aria-hidden="true" />
    <span class="min-w-0 flex-1 basis-60">{{ t(status === "frozen" ? "tenant.hold.scheduledFrozen" : "tenant.hold.scheduledSuspended", { app: name, date: fmt.date(hold.data.value?.scheduled_time, "dateTime") }) }}</span>
  </p>
</template>
