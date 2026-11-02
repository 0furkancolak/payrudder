<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { useOperatorSupportActions, useSupportHolidays, type SupportHoliday } from "./api";

/**
 * The days off of an app (operator): the first-answer clock of its tickets does not run on them, like a weekend. The list is saved whole. A ticket already open keeps its
 * deadline; changing its priority counts it again with the days as they are then.
 */
const props = defineProps<{ appId: string }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const holidays = useSupportHolidays(() => (open.value ? props.appId : ""));
const actions = useOperatorSupportActions();
const days = ref<SupportHoliday[]>([]);
const day = ref("");
const name = ref("");
// The list is the server's for the app shown: while it loads, and when the dialog opens again or for another app, it starts empty, never with the days of the last one.
watch(() => holidays.data.value, (d) => (days.value = d ? [...d.holidays] : []), { immediate: true });
watch(open, (isOpen) => {
  if (!isOpen) return;
  actions.saveHolidays.reset();
  day.value = name.value = "";
});

function add() {
  if (!day.value || days.value.some((h) => h.day === day.value)) return;
  days.value = [...days.value, { day: day.value, name: name.value.trim() }].sort((a, b) => a.day.localeCompare(b.day));
  day.value = name.value = "";
}
const save = () => actions.saveHolidays.mutate({ appId: props.appId, holidays: days.value });
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-xl" data-testid="holidays-dialog">
      <DialogHeader>
        <DialogTitle>{{ t("support.holidays.title", { app: holidays.data.value?.app_name ?? "…" }) }}</DialogTitle>
        <DialogDescription>{{ t("support.holidays.hint") }}</DialogDescription>
      </DialogHeader>
      <ErrorText :error="holidays.error.value" />
      <ul v-if="days.length > 0" class="divide-border/70 m-0 flex max-h-56 list-none flex-col divide-y overflow-y-auto p-0 text-sm" data-testid="holidays-list">
        <li v-for="h in days" :key="h.day" class="flex items-center justify-between gap-3 py-1.5" :data-testid="`holiday-${h.day}`">
          <span><span class="mono">{{ h.day }}</span><span v-if="h.name" class="text-muted-foreground" dir="auto"> · {{ h.name }}</span></span>
          <Button type="button" variant="ghost" size="sm" :aria-label="t('support.holidays.remove', { day: h.day })" :data-testid="`holiday-remove-${h.day}`" @click="days = days.filter((x) => x.day !== h.day)">×</Button>
        </li>
      </ul>
      <p v-else class="text-muted-foreground m-0 text-sm">{{ t("support.holidays.none") }}</p>
      <form class="flex flex-wrap items-end gap-2" @submit.prevent="add">
        <label class="grid gap-1 text-xs">{{ t("support.holidays.day") }}<Input v-model="day" type="date" data-testid="holiday-day" /></label>
        <label class="grid min-w-32 flex-1 gap-1 text-xs">{{ t("support.holidays.name") }}<Input v-model="name" maxlength="100" data-testid="holiday-name" /></label>
        <Button type="submit" variant="outline" size="sm" :disabled="!day" data-testid="holiday-add">{{ t("support.holidays.add") }}</Button>
      </form>
      <ErrorText :error="actions.saveHolidays.error.value" />
      <DialogFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("common.close") }}</Button>
        <Button type="button" :disabled="!holidays.data.value || actions.saveHolidays.isPending.value" data-testid="holidays-save" @click="save">{{ t("support.holidays.save") }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
