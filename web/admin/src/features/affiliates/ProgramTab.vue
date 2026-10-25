<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCoupons } from "@/features/pricing/api";
import { useTenantCurrency } from "@/features/tenants";
import { useCanWrite } from "@/shared/lib/access";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { useProgram, useSetProgram } from "./api";
import { COMMISSION_KINDS, defaultProgramForm, formToProgram, programToForm, PROGRAM_STATUSES, TERMS, type ProgramForm } from "./logic";

const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const canWrite = useCanWrite();
const tenantId = computed(() => props.tenantId);

const program = useProgram(tenantId);
const save = useSetProgram(tenantId);
const coupons = useCoupons(tenantId);
const tenantCurrency = useTenantCurrency(tenantId);
const fallback = computed(() => tenantCurrency.value || FALLBACK_CURRENCY);

const form = ref<ProgramForm>(defaultProgramForm(fallback.value));
const problem = ref("");
const saved = ref(false);

// The form starts from the stored program, or from the defaults before one exists.
watch(
  () => program.data.value,
  (p) => {
    if (program.isSuccess.value) form.value = programToForm(p, fallback.value);
  },
  { immediate: true },
);
// The tenant's currency arrives after the page: a form that still holds the stand-in switches to it.
watch(fallback, (now, before) => {
  if (form.value.currency === before) form.value.currency = now;
});

// The coupons a referred customer may get: the active ones, and the stored one even if it has since been deactivated.
const couponCodes = computed(() => {
  const codes = (coupons.data.value?.coupons ?? []).filter((c) => c.active).map((c) => c.code ?? "");
  if (form.value.coupon && !codes.includes(form.value.coupon)) codes.unshift(form.value.coupon);
  return codes;
});

function submit() {
  problem.value = "";
  saved.value = false;
  const r = formToProgram(form.value);
  if ("problem" in r) {
    problem.value = t(`affiliates.program.errors.${r.problem}`, { currency: form.value.currency });
    return;
  }
  save.mutate(r.body, { onSuccess: () => (saved.value = true) });
}
</script>

<template>
  <ErrorText :error="program.error.value" />
  <Skeleton v-if="program.isPending.value" class="h-64" />
  <Card v-else>
    <div>
      <h2 class="text-lg font-semibold">{{ t("affiliates.program.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ program.data.value === null ? t("affiliates.program.notSet") : t("affiliates.program.hint") }}</p>
      <p class="text-muted-foreground text-sm">{{ t("affiliates.program.appliesFromNow") }}</p>
    </div>
    <form class="grid gap-4 sm:grid-cols-2" :aria-label="t('affiliates.program.title')" @submit.prevent="submit">
      <fieldset :disabled="!canWrite || save.isPending.value" class="m-0 grid min-w-0 gap-4 border-0 p-0 sm:col-span-2 sm:grid-cols-2">
        <div class="grid content-start gap-1.5">
          <Label for="pg-status">{{ t("affiliates.program.status") }}</Label>
          <NativeSelect id="pg-status" v-model="form.status">
            <option v-for="s in PROGRAM_STATUSES" :key="s" :value="s">{{ t(`affiliates.status.program.${s}`) }}</option>
          </NativeSelect>
          <p class="text-muted-foreground text-xs">{{ t("affiliates.program.statusHint") }}</p>
        </div>

        <div class="grid content-start gap-1.5">
          <Label for="pg-kind">{{ t("affiliates.program.kind") }}</Label>
          <NativeSelect id="pg-kind" v-model="form.kind">
            <option v-for="k in COMMISSION_KINDS" :key="k" :value="k">{{ t(`affiliates.program.kinds.${k}`) }}</option>
          </NativeSelect>
        </div>

        <div v-if="form.kind === 'percent_bps'" class="grid content-start gap-1.5">
          <Label for="pg-percent">{{ t("affiliates.program.percent") }}</Label>
          <div class="flex items-center gap-2">
            <Input id="pg-percent" v-model="form.value" inputmode="decimal" autocomplete="off" class="max-w-32" />
            <span class="text-muted-foreground text-sm" aria-hidden="true">%</span>
          </div>
          <p class="text-muted-foreground text-xs">{{ t("affiliates.program.percentHint") }}</p>
        </div>
        <template v-else>
          <div class="grid content-start gap-1.5">
            <Label for="pg-fixed">{{ t("affiliates.program.fixed") }}</Label>
            <Input id="pg-fixed" v-model="form.value" inputmode="decimal" autocomplete="off" />
            <p class="text-muted-foreground text-xs">{{ t("affiliates.program.fixedHint") }}</p>
          </div>
        </template>

        <div class="grid content-start gap-1.5">
          <Label for="pg-currency">{{ t("affiliates.program.currency") }}</Label>
          <CurrencySelect id="pg-currency" v-model="form.currency" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.program.currencyHint") }}</p>
        </div>

        <div class="grid content-start gap-1.5">
          <Label for="pg-term">{{ t("affiliates.program.term") }}</Label>
          <NativeSelect id="pg-term" v-model="form.term">
            <option v-for="x in TERMS" :key="x" :value="x">{{ t(`affiliates.program.terms.${x}`) }}</option>
          </NativeSelect>
        </div>
        <div v-if="form.term !== 'lifetime'" class="grid content-start gap-1.5">
          <Label for="pg-term-value">{{ t(form.term === "months" ? "affiliates.program.termMonths" : "affiliates.program.termInvoices") }}</Label>
          <Input id="pg-term-value" v-model="form.termValue" inputmode="numeric" autocomplete="off" class="max-w-32" />
        </div>

        <div class="grid content-start gap-1.5">
          <Label for="pg-window">{{ t("affiliates.program.window") }}</Label>
          <Input id="pg-window" v-model="form.windowDays" inputmode="numeric" autocomplete="off" class="max-w-32" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.program.windowHint") }}</p>
        </div>
        <div class="grid content-start gap-1.5">
          <Label for="pg-hold">{{ t("affiliates.program.hold") }}</Label>
          <Input id="pg-hold" v-model="form.holdDays" inputmode="numeric" autocomplete="off" class="max-w-32" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.program.holdHint") }}</p>
        </div>

        <div class="grid content-start gap-1.5">
          <Label for="pg-min">{{ t("affiliates.program.minPayout", { currency: form.currency }) }}</Label>
          <Input id="pg-min" v-model="form.minPayout" inputmode="decimal" autocomplete="off" class="max-w-48" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.program.minPayoutHint") }}</p>
        </div>
        <div class="grid content-start gap-1.5">
          <Label for="pg-coupon">{{ t("affiliates.program.coupon") }}</Label>
          <NativeSelect id="pg-coupon" v-model="form.coupon">
            <option value="">{{ t("affiliates.program.noCoupon") }}</option>
            <option v-for="c in couponCodes" :key="c" :value="c">{{ c }}</option>
          </NativeSelect>
          <p class="text-muted-foreground text-xs">{{ t("affiliates.program.couponHint") }}</p>
        </div>

        <div class="grid content-start gap-1.5">
          <Label for="pg-cap">{{ t("affiliates.program.clickCap") }}</Label>
          <Input id="pg-cap" v-model="form.clickCap" inputmode="numeric" autocomplete="off" class="max-w-32" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.program.clickCapHint") }}</p>
        </div>
        <div class="grid content-start gap-1.5">
          <Label for="pg-landing">{{ t("affiliates.program.landing") }}</Label>
          <Input id="pg-landing" v-model="form.landingUrl" type="url" inputmode="url" autocomplete="off" placeholder="https://" dir="ltr" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.program.landingHint") }}</p>
        </div>

        <div class="flex items-start gap-3 sm:col-span-2">
          <Checkbox id="pg-auto" :model-value="form.autoApprove" @update:model-value="form.autoApprove = $event === true" />
          <div class="grid gap-1">
            <Label for="pg-auto">{{ t("affiliates.program.autoApprove") }}</Label>
            <p class="text-muted-foreground text-xs">{{ t("affiliates.program.autoApproveHint") }}</p>
          </div>
        </div>
      </fieldset>

      <div class="flex flex-col gap-2 sm:col-span-2">
        <ErrorText :message="problem" />
        <ErrorText :error="save.error.value" />
        <p v-if="saved && !save.isPending.value" class="text-success text-sm" role="status">{{ t("affiliates.program.saved") }}</p>
        <div>
          <Button type="submit" :disabled="!canWrite || save.isPending.value">{{ t("affiliates.program.save") }}</Button>
        </div>
      </div>
    </form>
  </Card>
</template>
