<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { firstError } from "@/shared/lib/errors";
import { ErrorText } from "@/shared/ui/error-text";
import { Card } from "@/shared/ui/card";
import { MAIL_CATEGORIES, useMailSettings, useSetMailCategory, type MailCategory } from "./api";

const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const settings = useMailSettings(tenantId);
const set = useSetMailCategory(tenantId);

/** The state of every category; a category the server did not list counts as on, which is the default. */
const enabled = computed(() => {
  const state = new Map<string, boolean>();
  for (const c of settings.data.value?.categories ?? []) if (c.category) state.set(c.category, c.enabled ?? true);
  return (category: MailCategory) => state.get(category) ?? true;
});
const failure = computed(() => firstError(settings, set));

function toggle(category: MailCategory, event: Event) {
  set.mutate({ category, enabled: (event.target as HTMLInputElement).checked });
}
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.mail.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.mail.hint") }}</p>
    <ul class="grid gap-3" :aria-label="t('billing.mail.title')">
      <li v-for="category in MAIL_CATEGORIES" :key="category">
        <label class="flex items-start gap-3">
          <input
            type="checkbox"
            class="mt-1 size-4"
            :data-testid="`mail-${category}`"
            :checked="enabled(category)"
            :disabled="settings.isPending.value || set.isPending.value"
            @change="toggle(category, $event)"
          />
          <span>
            <span class="block text-sm font-medium">{{ t(`billing.mail.categories.${category}`) }}</span>
            <span class="text-muted-foreground block text-xs">{{ t(`billing.mail.descriptions.${category}`) }}</span>
          </span>
        </label>
      </li>
    </ul>
    <p class="text-muted-foreground text-xs">{{ t("billing.mail.providerNote") }}</p>
    <ErrorText :error="failure" />
  </Card>
</template>
