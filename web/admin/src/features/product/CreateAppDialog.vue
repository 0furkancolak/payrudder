<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { Currency } from "@/shared/lib/currencies";
import { ownZone } from "@/shared/lib/intl";
import { FormField } from "@/shared/ui/form-field";
import { ErrorText } from "@/shared/ui/error-text";
import UpgradeHint from "@/features/saas/UpgradeHint.vue";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { CurrencySelect, TimezoneSelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useCreateApp } from "./api";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";

const { t } = useI18n();
const navigate = useNavigate();
const open = defineModel<boolean>("open", { required: true });
const create = useCreateApp();

const name = ref("");
const currency = ref<Currency>(FALLBACK_CURRENCY);
const timezone = ref(ownZone());
const ready = computed(() => name.value.trim() !== "");

// A dialog that was closed opens clean: the last name and error are not shown again.
watch(open, (v) => {
  if (v) {
    name.value = "";
    create.reset();
  }
});

function submit() {
  if (!ready.value) return;
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
        <form id="create-app" class="flex flex-col gap-4" @submit.prevent="submit">
          <FormField for="ca-name" :label="t('product.createApp.name')">
            <Input id="ca-name" v-model="name" autocomplete="off" required />
          </FormField>
          <div class="grid gap-4">
            <FormField for="ca-currency" :label="t('product.createApp.currency')">
              <CurrencySelect id="ca-currency" v-model="currency" :aria-label="t('product.createApp.currency')" />
            </FormField>
            <FormField for="ca-tz" :label="t('product.createApp.timezone')">
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
        <Button type="submit" form="create-app" :disabled="create.isPending.value || !ready">{{ t("product.createApp.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
