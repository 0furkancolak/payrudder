<script setup lang="ts">
import { Pencil } from "@lucide/vue";
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useTenant } from "@/features/tenants";
import { addressLines, ADDRESS_MAX, cleanAddress, countryProfile, postalCodeLooksOff, validateAddress, type Address, type AddressPart } from "@/shared/lib/address";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { LocationFields } from "@/shared/ui/location-fields";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useSetSellerAddress } from "./api";

const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(tenantId);
const save = useSetSellerAddress(tenantId);

const form = ref<Address>({});
const saved = ref(false);
const open = ref(false);

function load(a: Address | undefined) {
  form.value = { ...(a ?? {}) };
}
// The stored address fills the form once the tenant arrives, and again after a save refreshes it.
watch(() => tenant.data.value?.address, (a) => load(a), { immediate: true });

// Closing the sheet drops what was typed and not saved, so the preview shows the stored address again.
watch(open, (v) => {
  if (v || saved.value) return;
  load(tenant.data.value?.address);
  save.reset();
});

const cleaned = computed(() => cleanAddress(form.value));
const problems = computed(() => validateAddress(cleaned.value));
const valid = computed(() => Object.keys(problems.value).length === 0);
const profile = computed(() => countryProfile(cleaned.value.country));
const postalOff = computed(() => postalCodeLooksOff(cleaned.value.country, cleaned.value.postal_code));
const lines = computed(() => addressLines(cleaned.value));
const problemText = (part: AddressPart) => {
  const p = problems.value[part];
  return p ? t(`address.problems.${p}`, { max: ADDRESS_MAX[part] }) : "";
};
const placeProblems = computed(() => ({ country: problemText("country"), region: problemText("region"), city: problemText("city") }));
// A place picked from a list types nothing into the form, so the "saved" note goes away here too (only while editing: a save reloads the form).
watch(
  () => [form.value.country, form.value.region, form.value.city],
  () => {
    if (open.value) saved.value = false;
  },
);

function submit() {
  if (!valid.value) return;
  saved.value = false;
  save.mutate(cleaned.value, {
    onSuccess: () => {
      saved.value = true;
      open.value = false;
    },
  });
}
</script>

<template>
  <Card role="region" :aria-label="t('address.preview.title')">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <p class="text-muted-foreground max-w-xl text-sm">{{ t("address.hint") }} {{ t("address.preview.hint") }}</p>
      <Button type="button" @click="open = true"><Pencil class="size-4" aria-hidden="true" />{{ t("address.edit") }}</Button>
    </div>
    <div class="bg-card max-w-md rounded-2xl border p-4 text-sm shadow-sm" data-testid="invoice-preview">
      <p class="text-muted-foreground text-xs tracking-wide uppercase">{{ t("address.preview.from") }}</p>
      <p class="font-semibold">{{ tenant.data.value?.name }}</p>
      <p v-for="(line, i) in lines" :key="i" data-testid="preview-line">{{ line }}</p>
      <p v-if="lines.length === 0" class="text-muted-foreground italic">{{ t("address.preview.empty") }}</p>
    </div>
    <p v-if="saved" class="text-success text-sm" role="status">{{ t("address.saved") }}</p>
    <ErrorText :error="tenant.error.value" />

    <Sheet v-model:open="open">
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{{ t("address.title") }}</SheetTitle>
          <SheetDescription>{{ t("address.hint") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
      <form id="seller-address-form" class="grid gap-4" role="form" :aria-label="t('address.formLabel')" @submit.prevent="submit" @input="saved = false">
        <LocationFields v-model:country="form.country" v-model:region="form.region" v-model:city="form.city" id-prefix="seller" :problems="placeProblems" />

        <div class="grid gap-1">
          <label class="text-sm" for="seller-line1">{{ t("address.labels.line1") }}</label>
          <Input id="seller-line1" v-model="form.line1" :aria-invalid="!!problems.line1" />
          <p v-if="problems.line1" class="text-destructive text-xs" role="alert">{{ problemText("line1") }}</p>
        </div>
        <div class="grid gap-1">
          <label class="text-sm" for="seller-line2">{{ t("address.labels.line2") }}</label>
          <Input id="seller-line2" v-model="form.line2" :aria-invalid="!!problems.line2" />
          <p v-if="problems.line2" class="text-destructive text-xs" role="alert">{{ problemText("line2") }}</p>
        </div>
        <div class="grid gap-1 sm:max-w-[50%]">
          <label class="text-sm" for="seller-postal">{{ t(`address.labels.${profile.postalCode}`) }}</label>
          <Input id="seller-postal" v-model="form.postal_code" :placeholder="profile.example" :aria-invalid="!!problems.postal_code" />
          <p v-if="problems.postal_code" class="text-destructive text-xs" role="alert">{{ problemText("postal_code") }}</p>
          <p v-else-if="postalOff" class="text-muted-foreground text-xs" role="status">{{ t("address.postalHint", { example: profile.example ?? "" }) }}</p>
        </div>
        <ErrorText :error="save.error.value" />
      </form>
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="seller-address-form" :disabled="save.isPending.value || !valid">{{ t("address.save") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  </Card>
</template>
