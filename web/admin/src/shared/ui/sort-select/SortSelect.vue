<script setup lang="ts">
import { ArrowUpDown } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { NativeSelect } from "@/shared/ui/native-select";

/**
 * The order of a table in one select: each choice is a column and a direction together ("Newest first", "Name, A to Z"). The first entry is the order the list
 * arrives in. Choosing applies at once (a sort loses nothing, unlike a filter).
 */
defineProps<{ options: readonly { key: string; label: string }[] }>();
const model = defineModel<string>({ default: "" });
const { t } = useI18n();
</script>

<template>
  <label class="text-muted-foreground inline-flex items-center gap-1.5 text-sm">
    <ArrowUpDown class="size-4" aria-hidden="true" />
    <span class="sr-only">{{ t("common.sort.label") }}</span>
    <NativeSelect v-model="model" :aria-label="t('common.sort.label')" data-testid="sort-select">
      <option value="">{{ t("common.sort.default") }}</option>
      <option v-for="o in options" :key="o.key" :value="o.key">{{ o.label }}</option>
    </NativeSelect>
  </label>
</template>
