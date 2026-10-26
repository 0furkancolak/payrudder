<script setup lang="ts">
import { Plus, X } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useSaveMeter, type Meter } from "./api";
import { AGGREGATIONS, aggregationKey, emptyMeterForm, formOfMeter, toMeterBody } from "./meter";

/**
 * The form of a meter in a sheet. Without `base` it makes a new meter; with one it saves the next version of that meter (the code and the scale are fixed
 * per code, so they are shown and not edited).
 */
const props = defineProps<{ tenantId: string; base?: Meter | null }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();

const save = useSaveMeter(() => props.tenantId);
const form = ref(emptyMeterForm());
const error = ref("");

// A sheet that was closed opens clean (or filled with the meter it saves a version of).
watch(open, (v) => {
  if (!v) return;
  form.value = props.base ? formOfMeter(props.base as Parameters<typeof formOfMeter>[0]) : emptyMeterForm();
  error.value = "";
  save.reset();
});

const isVersion = computed(() => !!props.base);
const counting = computed(() => form.value.aggregation === "AGGREGATION_COUNT");

function submit() {
  error.value = "";
  const r = toMeterBody(form.value);
  if ("error" in r) {
    error.value = t(`billing.usage.meters.errors.${r.error}`);
    return;
  }
  save.mutate(r.body, { onSuccess: () => (open.value = false) });
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ isVersion ? t("billing.usage.meters.versionTitle", { code: base?.code }) : t("billing.usage.meters.new") }}</SheetTitle>
        <SheetDescription>{{ isVersion ? t("billing.usage.meters.versionHint", { version: base?.version ?? 1 }) : t("billing.usage.meters.newHint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="meter-form" class="grid gap-5" role="form" :aria-label="t('billing.usage.meters.formLabel')" @submit.prevent="submit">
          <div class="grid gap-1.5">
            <Label for="meter-code">{{ t("billing.usage.meters.code") }}</Label>
            <Input id="meter-code" v-model="form.code" :disabled="isVersion" class="mono" autocomplete="off" placeholder="api_calls" required />
            <p class="text-muted-foreground text-xs">{{ t("billing.usage.meters.codeHint") }}</p>
          </div>
          <div class="grid gap-1.5">
            <Label for="meter-unit">{{ t("billing.usage.meters.unit") }}</Label>
            <Input id="meter-unit" v-model="form.unit" maxlength="32" :placeholder="t('billing.usage.meters.unitPlaceholder')" required />
          </div>
          <div class="grid gap-1.5">
            <Label for="meter-agg">{{ t("billing.usage.meters.aggregation") }}</Label>
            <NativeSelect id="meter-agg" v-model="form.aggregation">
              <option v-for="a in AGGREGATIONS" :key="a" :value="a">{{ t(`billing.usage.aggregation.${aggregationKey(a)}`) }}</option>
            </NativeSelect>
            <p class="text-muted-foreground text-xs">{{ t(`billing.usage.aggregationHint.${aggregationKey(form.aggregation)}`) }}</p>
          </div>
          <div v-if="!counting" class="grid gap-1.5">
            <Label for="meter-prop">{{ t("billing.usage.meters.property") }}</Label>
            <Input id="meter-prop" v-model="form.property" class="mono" autocomplete="off" placeholder="tokens" />
            <p class="text-muted-foreground text-xs">{{ t("billing.usage.meters.propertyHint") }}</p>
          </div>
          <div class="grid gap-1.5">
            <Label for="meter-scale">{{ t("billing.usage.meters.scale") }}</Label>
            <Input id="meter-scale" v-model="form.scale" inputmode="numeric" class="max-w-24" :disabled="isVersion" />
            <p class="text-muted-foreground text-xs">{{ isVersion ? t("billing.usage.meters.scaleFixed") : t("billing.usage.meters.scaleHint") }}</p>
          </div>
          <fieldset class="m-0 grid min-w-0 gap-2 border-0 p-0">
            <legend class="mb-1 text-sm font-medium">{{ t("billing.usage.meters.filter") }}</legend>
            <p class="text-muted-foreground text-xs">{{ t("billing.usage.meters.filterHint") }}</p>
            <div v-for="(r, i) in form.filters" :key="i" class="grid grid-cols-[1fr_1.5fr_auto] items-center gap-2" role="group" :aria-label="t('billing.usage.meters.filterRow', { n: i + 1 })">
              <Input v-model="r.property" class="mono" :aria-label="t('billing.usage.meters.filterProperty', { n: i + 1 })" :placeholder="t('billing.usage.meters.filterProperty', { n: i + 1 })" />
              <Input v-model="r.values" :aria-label="t('billing.usage.meters.filterValues', { n: i + 1 })" :placeholder="t('billing.usage.meters.filterValuesPlaceholder')" />
              <Button type="button" variant="ghost" size="icon" :aria-label="t('billing.common.remove')" @click="form.filters.splice(i, 1)"><X class="size-4" /></Button>
            </div>
            <div><Button type="button" variant="outline" size="sm" :disabled="form.filters.length >= 10" @click="form.filters.push({ property: '', values: '' })"><Plus class="size-4" aria-hidden="true" />{{ t("billing.usage.meters.addFilter") }}</Button></div>
          </fieldset>
          <ErrorText :message="error" />
          <ErrorText :error="save.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="meter-form" :disabled="save.isPending.value">{{ isVersion ? t("billing.usage.meters.saveVersion") : t("billing.usage.meters.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
