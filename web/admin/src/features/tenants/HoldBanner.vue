<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { CirclePause } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import type { Hold } from "./hold";

/**
 * "Your app is on hold": shown to the people of an organisation on every page of the console while the operator has the live environment of one of its apps
 * suspended, frozen or banned (plan 14). It says what stops, that the data is kept, and where to write (the help page is also the only appeal there is). The reason
 * is the operator's note and is never shown here, as it is never in the mail.
 */
defineProps<{ held: { id: string; name: string; hold: Hold }[] }>();
const { t } = useI18n();
</script>

<template>
  <div v-if="held.length > 0" class="mb-6 flex flex-col gap-2" data-testid="hold-banner">
    <p
      v-for="h in held"
      :key="h.id"
      :class="['m-0 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-2xl px-4 py-3 text-sm', h.hold === 'frozen' ? 'bg-warning-soft text-warning' : 'bg-destructive-soft text-destructive']"
      role="status"
      :data-testid="`hold-${h.hold}`"
    >
      <CirclePause class="size-4 shrink-0" aria-hidden="true" />
      <span class="min-w-0 flex-1 basis-60">{{ t(`tenant.hold.${h.hold}`, { app: h.name }) }}</span>
      <Link to="/app/support" class="font-medium underline underline-offset-4" data-testid="hold-support">{{ t("tenant.hold.write") }}</Link>
    </p>
  </div>
</template>
