<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { Link } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import type { Currency } from "@/shared/lib/currencies";
import { currencyList, ownZone, zoneIds } from "@/shared/lib/intl";
import { useFormErrors } from "@/shared/lib/formErrors";
import { usePlan } from "@/features/saas/api";
import { appLimitReached } from "@/features/saas/lib";
import { FormField } from "@/shared/ui/form-field";
import { ErrorText } from "@/shared/ui/error-text";
import UpgradeHint from "@/features/saas/UpgradeHint.vue";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { CurrencySelect, TimezoneSelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useCreateApp } from "./api";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";

const { t, locale } = useI18n();
const navigate = useNavigate();
const open = defineModel<boolean>("open", { required: true });
const create = useCreateApp();

const name = ref("");
const currency = ref<Currency>(FALLBACK_CURRENCY);
const timezone = ref(ownZone());
const plan = usePlan();
/** The plan's app limit when it is used up: said before the form is filled in, with the way out, instead of after "Create". */
const limit = computed(() => appLimitReached(plan.data.value?.quotas));
const currencies = computed(() => new Set<string>(currencyList(locale.value).map((c) => c.code)));
const zones = computed(() => new Set<string>(zoneIds()));
const check = useFormErrors(() => ({
  name: name.value.trim() === "" ? t("common.validation.required") : undefined,
  currency: currencies.value.has(currency.value) ? undefined : t("product.createApp.badCurrency"),
  timezone: zones.value.has(timezone.value) ? undefined : t("product.createApp.badTimezone"),
}));
const errs = check.errors;
watch([name, currency, timezone], () => create.isError.value && create.reset());

// A dialog that was closed opens clean: the last name and error are not shown again.
watch(open, (v) => {
  if (v) {
    name.value = "";
    create.reset();
    check.reset();
  }
});

function submit() {
  if (limit.value || !check.attempt()) return;
  create.mutate(
    { name: name.value.trim(), currency: currency.value, timezone: timezone.value },
    {
      onSuccess: async (app) => {
        open.value = false;
        if (app.id) await navigate({ to: "/app/apps/$appId", params: { appId: app.id } });
      },
    },
  );
}
</script>

<template>
  <Sheet v-model:open="open" name="new-app">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("product.createApp.title") }}</SheetTitle>
        <SheetDescription>{{ t("product.createApp.intro") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <p v-if="limit" class="bg-warning-soft text-warning mb-4 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="app-limit">
          {{ t("product.createApp.limitReached", { used: limit.used, limit: limit.limit }) }}
          <Link to="/app/settings/plan" class="font-medium underline underline-offset-2">{{ t("saas.upgrade") }}</Link>
        </p>
        <form id="create-app" class="flex flex-col gap-4" novalidate @submit.prevent="submit">
          <FormField for="ca-name" :label="t('product.createApp.name')" :error="errs.name">
            <Input id="ca-name" v-model="name" autocomplete="off" :disabled="!!limit" :aria-invalid="errs.name ? true : undefined" aria-describedby="ca-name-error" />
          </FormField>
          <div class="grid gap-4">
            <FormField for="ca-currency" :label="t('product.createApp.currency')" :error="errs.currency">
              <CurrencySelect id="ca-currency" v-model="currency" :aria-label="t('product.createApp.currency')" />
            </FormField>
            <FormField for="ca-tz" :label="t('product.createApp.timezone')" :error="errs.timezone">
              <TimezoneSelect id="ca-tz" v-model="timezone" :aria-label="t('product.createApp.timezone')" />
            </FormField>
          </div>
          <p class="text-muted-foreground text-xs">{{ t("product.createApp.immutable") }}</p>
          <ErrorText :error="create.error.value" box />
          <UpgradeHint :error="create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-app" :disabled="create.isPending.value || !!limit" :title="limit ? t('product.createApp.limitReached', { used: limit.used, limit: limit.limit }) : undefined">{{ t("product.createApp.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
