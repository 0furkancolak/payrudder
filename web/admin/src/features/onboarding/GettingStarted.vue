<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ArrowRight, Check, X } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { App } from "@/features/product/api";
import { Button } from "@/shared/ui/button";
import { useSetupFacts } from "./api";
import { checklist, doneCount, type Item } from "./checklist";
import { hideChecklist, patchProgress, progress, resumeOnboarding } from "./state";

/**
 * The "Getting started" card of the organisation's home: what is left of the first setup, ticked from the person's real data (a meter made on the
 * Meters page counts as much as one the wizard made). Each open item goes to the page where it is done. It goes away when everything is done, or when the
 * person hides it.
 */
const props = defineProps<{ app: App | undefined }>();
const emit = defineEmits<{ createApp: [] }>();
const { t } = useI18n();
const { facts, tenant } = useSetupFacts(() => props.app);

const rows = computed(() => checklist(facts.value));
const done = computed(() => doneCount(rows.value));
const complete = computed(() => done.value === rows.value.length);
const appId = computed(() => props.app?.id ?? "");
const target: (item: Item) => { to: string; search?: Record<string, string> } | null = (item) => {
  switch (item) {
    case "meter":
      return tenant.value ? { to: `/app/t/${tenant.value}/meters` } : null;
    case "package":
      return tenant.value ? { to: `/app/t/${tenant.value}/packages` } : null;
    case "event":
      return tenant.value ? { to: `/app/t/${tenant.value}/usage` } : null;
    case "key":
      return appId.value ? { to: `/app/apps/${appId.value}/keys`, search: { env: "sandbox" } } : null;
    case "live":
      return appId.value ? { to: `/app/apps/${appId.value}/go-live`, search: { env: "live" } } : null;
    default:
      return null;
  }
};
/** The test event is sent from the wizard's own step, which needs the meter and the package it made. */
const canSendTest = computed(() => !!progress.value?.meter && !!progress.value?.plan);
function sendTest() {
  patchProgress({ status: "active", step: "event" });
}
const wizardUnfinished = computed(() => progress.value?.status !== "done");
</script>

<template>
  <section v-if="!complete" class="surface flex flex-col gap-4 p-5 md:p-6" :aria-label="t('onboarding.checklist.title')" data-testid="getting-started">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex min-w-0 flex-col gap-1">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("onboarding.checklist.title") }}</h2>
        <p class="text-muted-foreground text-sm" role="status" data-testid="getting-started-count">{{ t("onboarding.checklist.progress", { done, total: rows.length }) }}</p>
      </div>
      <div class="flex items-center gap-2">
        <Button v-if="wizardUnfinished" type="button" size="sm" data-testid="getting-started-resume" @click="resumeOnboarding()">{{ t("onboarding.checklist.resume") }}</Button>
        <Button type="button" variant="ghost" size="icon-sm" :aria-label="t('onboarding.checklist.hide')" :title="t('onboarding.checklist.hide')" data-testid="getting-started-hide" @click="hideChecklist()"><X class="size-4" aria-hidden="true" /></Button>
      </div>
    </header>
    <div class="bg-muted h-1.5 overflow-hidden rounded-full" aria-hidden="true"><div class="bg-brand h-full rounded-full transition-all" :style="{ width: `${(done / rows.length) * 100}%` }"></div></div>
    <ul class="divide-border/70 m-0 flex list-none flex-col divide-y p-0">
      <li v-for="r in rows" :key="r.key" class="flex items-center gap-3 py-2.5" :data-testid="`todo-${r.key}`" :data-done="r.done">
        <span :class="['flex size-6 shrink-0 items-center justify-center rounded-full border', r.done ? 'border-success bg-success text-white' : 'border-border']" aria-hidden="true"><Check v-if="r.done" class="size-3.5" /></span>
        <span :class="['flex-1 text-sm', r.done ? 'text-muted-foreground line-through' : 'font-medium']">{{ t(`onboarding.checklist.items.${r.key}`) }}<span class="sr-only"> — {{ r.done ? t("onboarding.checklist.done") : t("onboarding.checklist.todo") }}</span></span>
        <template v-if="!r.done">
          <Button v-if="r.key === 'app'" type="button" variant="outline" size="sm" @click="emit('createApp')">{{ t("onboarding.checklist.open") }}</Button>
          <Button v-else-if="r.key === 'event' && canSendTest" type="button" variant="outline" size="sm" data-testid="todo-event-send" @click="sendTest">{{ t("onboarding.checklist.open") }}</Button>
          <Link v-else-if="target(r.key)" :to="target(r.key)!.to" :search="target(r.key)!.search as never" class="text-brand-strong inline-flex items-center gap-1 text-sm font-medium underline-offset-4 hover:underline">
            {{ t("onboarding.checklist.open") }}<ArrowRight class="size-3.5 rtl:rotate-180" aria-hidden="true" />
          </Link>
        </template>
      </li>
    </ul>
  </section>
</template>
