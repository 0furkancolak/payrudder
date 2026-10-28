<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { firstError } from "@/shared/lib/errors";
import { ErrorText } from "@/shared/ui/error-text";
import { Card } from "@/shared/ui/card";
import { Switch } from "@/shared/ui/switch";
import { MAIL_CATEGORIES, useMailSettings, useSetMailCategory, type MailCategory } from "./api";
import { useRouteTenantId } from "@/shared/lib/routeTenant";

const { t } = useI18n();
const tenantId = useRouteTenantId();
const settings = useMailSettings(tenantId);
const set = useSetMailCategory(tenantId);

/** The state of every category; a category the server did not list counts as on, which is the default. */
const enabled = computed(() => {
  const state = new Map<string, boolean>();
  for (const c of settings.data.value?.categories ?? []) if (c.category) state.set(c.category, c.enabled ?? true);
  return (category: MailCategory) => state.get(category) ?? true;
});
const failure = computed(() => firstError(settings, set));

function toggle(category: MailCategory, on: boolean) {
  set.mutate({ category, enabled: on });
}
</script>

<template>
  <Card class="gap-0 p-3">
    <p class="text-muted-foreground px-3 pt-2 pb-3 text-sm">{{ t("billing.mail.hint") }}</p>
    <ul class="divide-y" :aria-label="t('billing.mail.title')">
      <li v-for="category in MAIL_CATEGORIES" :key="category" class="flex items-center justify-between gap-4 px-3 py-3.5">
        <label :for="`mail-${category}`" class="min-w-0 flex-1 cursor-pointer">
          <span class="block text-sm font-medium">{{ t(`billing.mail.categories.${category}`) }}</span>
          <span class="text-muted-foreground block text-xs">{{ t(`billing.mail.descriptions.${category}`) }}</span>
        </label>
        <Switch :id="`mail-${category}`" :data-testid="`mail-${category}`" :model-value="enabled(category)" :disabled="settings.isPending.value || set.isPending.value" @update:model-value="toggle(category, $event)" />
      </li>
    </ul>
    <p class="text-muted-foreground px-3 pt-3 text-xs">{{ t("billing.mail.providerNote") }}</p>
    <ErrorText :error="failure" class="px-3" />
  </Card>
</template>
