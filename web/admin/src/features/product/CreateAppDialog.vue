<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { Currency } from "@/shared/lib/currencies";
import { ownZone } from "@/shared/lib/intl";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { CurrencySelect, TimezoneSelect } from "@/shared/ui/search-select";
import { useCreateApp } from "./api";

const { t } = useI18n();
const errorText = useErrorText();
const navigate = useNavigate();
const open = defineModel<boolean>("open", { required: true });
const create = useCreateApp();

const name = ref("");
const currency = ref<Currency>("USD");
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
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{{ t("product.createApp.title") }}</DialogTitle>
        <DialogDescription>{{ t("product.createApp.intro") }}</DialogDescription>
      </DialogHeader>
      <form id="create-app" class="flex flex-col gap-4" @submit.prevent="submit">
        <div class="flex flex-col gap-1.5">
          <Label for="ca-name">{{ t("product.createApp.name") }}</Label>
          <Input id="ca-name" v-model="name" autocomplete="off" required />
        </div>
        <div class="grid gap-4">
          <div class="flex flex-col gap-1.5">
            <Label for="ca-currency">{{ t("product.createApp.currency") }}</Label>
            <CurrencySelect id="ca-currency" v-model="currency" :aria-label="t('product.createApp.currency')" />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="ca-tz">{{ t("product.createApp.timezone") }}</Label>
            <TimezoneSelect id="ca-tz" v-model="timezone" :aria-label="t('product.createApp.timezone')" />
          </div>
        </div>
        <p class="text-muted-foreground text-xs">{{ t("product.createApp.immutable") }}</p>
        <p v-if="create.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(create.error.value) }}</p>
      </form>
      <DialogFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-app" :disabled="create.isPending.value || !ready">{{ t("product.createApp.submit") }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
