<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import type { ApiError } from "@/shared/api/client";

/** The files that could not be attached to a message that was already sent, each with the reason in the reader's language. */
defineProps<{ failures: readonly { name: string; error: ApiError }[]; note?: string }>();
const { t } = useI18n();
const errorText = useErrorText();
</script>

<template>
  <div v-if="failures.length > 0" role="alert" class="bg-destructive-soft text-destructive grid gap-1 rounded-2xl px-4 py-3 text-sm" data-testid="attach-failures">
    <p v-if="note">{{ note }}</p>
    <p v-else>{{ t("support.attach.failed") }}</p>
    <ul class="list-disc ps-5">
      <li v-for="f in failures" :key="f.name">{{ f.name }}: {{ errorText(f.error) }}</li>
    </ul>
  </div>
</template>
