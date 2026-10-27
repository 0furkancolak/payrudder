<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { isUpgradeError } from "./lib";

/** Under an ErrorText: when the error is "your plan's limit is reached", a link to the plan page. It draws nothing for any other error. */
defineProps<{ error?: unknown }>();
const { t } = useI18n();
</script>

<template>
  <p v-if="isUpgradeError(error)" class="text-sm" data-testid="upgrade-hint">
    <Link to="/app/settings/plan" class="text-brand-strong font-medium underline underline-offset-2">{{ t("saas.upgrade") }}</Link>
  </p>
</template>
