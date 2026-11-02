<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { PageHeader } from "@/shared/ui/page-header";
import BlocklistTable from "./BlocklistTable.vue";

/** The operator's platform-wide blocklist: held against every app, and no app is told that it was this list that refused. */
const { t } = useI18n();
</script>

<template>
  <PageHeader :title="t('shell.items.platformBlocklist')" />
  <BlocklistTable platform />
</template>
