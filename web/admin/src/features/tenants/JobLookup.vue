<script setup lang="ts">
import { useMutation } from "@tanstack/vue-query";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { lookupJob } from "./api";

const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const jobId = ref("");

const look = useMutation({
  mutationFn: (kind: "replay" | "rerating") => lookupJob(kind, props.tenantId, jobId.value.trim()),
});
</script>

<template>
  <form class="row" @submit.prevent="look.mutate('replay')">
    <input v-model="jobId" :aria-label="t('tenant.jobs.idLabel')" :placeholder="t('tenant.jobs.idLabel')" />
    <button type="submit" :disabled="jobId.trim() === ''">{{ t("tenant.jobs.replay") }}</button>
    <button type="button" :disabled="jobId.trim() === ''" @click="look.mutate('rerating')">{{ t("tenant.jobs.rerating") }}</button>
  </form>
  <p v-if="look.error.value" class="error">{{ toApiError(look.error.value).message }}</p>
  <pre v-if="look.data.value" class="mono">{{ JSON.stringify(look.data.value, null, 2) }}</pre>
</template>
