<script setup lang="ts">
import { Pencil, Trash2 } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useDeleteOverride, useMeters, useOverrides, useSetOverride } from "@/features/meters/api";
import QuotaFields from "@/features/meters/QuotaFields.vue";
import { modeWord, newQuota, quotaDraftOf, quotaTerms, scaleOf, windowWord, type QuotaDraft } from "@/features/meters/quota";
import EntitlementsTable from "@/features/subscriptions/EntitlementsTable.vue";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";

/**
 * One customer's own limits: an override replaces what its package allows on a meter (or limits a meter the package does not), for every subscription
 * of the customer, until it is deleted. The usage below shows what is in force now.
 */
const props = defineProps<{ tenantId: string; customer: string }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const canWrite = useCanWrite();
const meters = useMeters(() => props.tenantId);
const meterList = computed(() => meters.data.value ?? []);
const overrides = useOverrides(
  () => props.tenantId,
  () => props.customer,
);
const save = useSetOverride(
  () => props.tenantId,
  () => props.customer,
);
const remove = useDeleteOverride(
  () => props.tenantId,
  () => props.customer,
);

const meter = ref("");
const draft = ref<QuotaDraft>(newQuota());
const formError = ref("");
const unit = computed(() => meterList.value.find((m) => m.code === meter.value)?.unit_label ?? "");

watch(open, (v) => {
  if (v) {
    meter.value = "";
    draft.value = newQuota();
    formError.value = "";
    save.reset();
    remove.reset();
  }
});
// Choosing a meter that has an override already edits it.
watch(meter, (m) => {
  const o = (overrides.data.value ?? []).find((x) => x.meter_code === m);
  draft.value = o ? quotaDraftOf(o) : newQuota();
});

function submit() {
  formError.value = "";
  if (meter.value === "") {
    formError.value = t("billing.quotas.chooseMeter");
    return;
  }
  const r = quotaTerms(draft.value, scaleOf(meterList.value, meter.value));
  if ("error" in r) {
    formError.value = t(`billing.quotas.errors.${r.error}`, { n: scaleOf(meterList.value, meter.value) });
    return;
  }
  save.mutate({ meter: meter.value, terms: r.terms }, { onSuccess: () => (meter.value = "") });
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="sm:max-w-2xl">
      <SheetHeader>
        <SheetTitle>{{ t("billing.quotas.of", { id: customer }) }}</SheetTitle>
        <SheetDescription>{{ t("billing.quotas.intro") }}</SheetDescription>
      </SheetHeader>
      <SheetBody class="flex flex-col gap-5">
        <section :aria-label="t('billing.quotas.overrides')">
          <h3 class="mb-2 text-sm font-semibold">{{ t("billing.quotas.overrides") }}</h3>
          <ErrorText :error="overrides.error.value" />
          <Table v-if="(overrides.data.value ?? []).length > 0">
            <TableHeader>
              <TableRow class="hover:bg-transparent">
                <TableHead>{{ t("billing.subscriptions.meter") }}</TableHead>
                <TableHead class="text-end">{{ t("billing.subscriptions.limit") }}</TableHead>
                <TableHead>{{ t("billing.quotas.windowColumn") }}</TableHead>
                <TableHead>{{ t("billing.quotas.modeColumn") }}</TableHead>
                <TableHead>{{ t("billing.quotas.alertsColumn") }}</TableHead>
                <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="o in overrides.data.value ?? []" :key="o.meter_code">
                <TableCell class="mono">{{ o.meter_code }}</TableCell>
                <TableCell class="text-end tabular-nums">{{ o.limit === "0" ? t("billing.subscriptions.unlimited") : o.limit }}</TableCell>
                <TableCell>{{ t(`billing.quotas.windows.${windowWord(o.window)}`) }}</TableCell>
                <TableCell><Badge variant="secondary">{{ t(`billing.quotas.modes.${modeWord(o.mode)}`) }}</Badge></TableCell>
                <TableCell class="tabular-nums">{{ (o.alert_thresholds_pct ?? []).map((p) => `${p}%`).join(" · ") }}</TableCell>
                <TableCell class="text-end whitespace-nowrap">
                  <template v-if="canWrite">
                    <Button variant="ghost" size="icon" :aria-label="t('billing.quotas.edit', { meter: o.meter_code ?? '' })" @click="meter = o.meter_code ?? ''"><Pencil class="size-4" /></Button>
                    <Button variant="ghost" size="icon" :disabled="remove.isPending.value" :aria-label="t('billing.quotas.remove', { meter: o.meter_code ?? '' })" @click="remove.mutate(o.meter_code ?? '')"><Trash2 class="size-4" /></Button>
                  </template>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
          <p v-else-if="!overrides.isPending.value && !overrides.error.value" class="text-muted-foreground text-sm">{{ t("billing.quotas.none") }}</p>
          <ErrorText :error="remove.error.value" />
        </section>

        <form v-if="canWrite" id="set-override" class="grid gap-3 border-t pt-4" role="form" :aria-label="t('billing.quotas.formLabel')" @submit.prevent="submit">
          <h3 class="text-sm font-semibold">{{ t("billing.quotas.set") }}</h3>
          <NativeSelect v-model="meter" :aria-label="t('billing.quotas.meter')">
            <option value="" disabled>{{ t("billing.quotas.chooseMeter") }}</option>
            <option v-for="m in meterList" :key="m.code" :value="m.code">{{ m.code }}</option>
          </NativeSelect>
          <QuotaFields v-model="draft" :unit="unit" />
          <ErrorText :message="formError" />
          <ErrorText :error="save.error.value" />
        </form>

        <EntitlementsTable :tenant-id="tenantId" :customer="customer" />
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("common.close") }}</Button>
        <Button v-if="canWrite" type="submit" form="set-override" :disabled="save.isPending.value">{{ t("billing.quotas.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
