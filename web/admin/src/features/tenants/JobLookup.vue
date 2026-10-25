<script setup lang="ts">
import { useMutation } from "@tanstack/vue-query";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { lookupJob } from "./api";

const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const jobId = ref("");

const look = useMutation({
  mutationFn: (kind: "replay" | "rerating") => lookupJob(kind, props.tenantId, jobId.value.trim()),
});
</script>

<template>
  <form class="flex flex-wrap items-center gap-2" @submit.prevent="look.mutate('replay')">
    <Input v-model="jobId" class="max-w-sm" :aria-label="t('tenant.jobs.idLabel')" :placeholder="t('tenant.jobs.idLabel')" />
    <Button type="submit" :disabled="jobId.trim() === ''">{{ t("tenant.jobs.replay") }}</Button>
    <Button type="button" variant="outline" :disabled="jobId.trim() === ''" @click="look.mutate('rerating')">{{ t("tenant.jobs.rerating") }}</Button>
  </form>
  <ErrorText :error="look.error.value" />
  <pre v-if="look.data.value" class="bg-muted mono overflow-x-auto rounded-2xl p-4 text-xs">{{ JSON.stringify(look.data.value, null, 2) }}</pre>
</template>
