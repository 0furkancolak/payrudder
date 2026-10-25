<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { Environment } from "@/shared/lib/environment";
import { SegmentedControl } from "@/shared/ui/segmented";

/**
 * The environment switch of the organisation's home. The home shows one environment at a time (it never adds live and sandbox together); the choice is
 * in the address (`?env=sandbox`), so a link shares it and a reload keeps it.
 */
const props = defineProps<{ env: Environment }>();
const { t } = useI18n();
const navigate = useNavigate();
const options = computed(() => [
  { value: "live", label: t("product.env.live") },
  { value: "sandbox", label: t("product.env.sandbox") },
]);

function pick(value: string) {
  if (value !== "live" && value !== "sandbox") return;
  // Live is written out too: with no env in the address the home would choose by itself, and the choice must stay the person's.
  void navigate({ to: "/app", search: { env: value } as never, replace: true });
}
</script>

<template>
  <SegmentedControl :model-value="props.env" :options="options" :label="t('product.env.label')" data-testid="env-switch" @update:model-value="pick" />
</template>
