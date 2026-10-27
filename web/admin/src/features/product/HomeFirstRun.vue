<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { GettingStarted, loadProgress, progress, wizardOpen } from "@/features/onboarding";
import { startTourOnce, welcomeOff } from "@/shared/ui/tour";
import { useAccount, useApps, useMe, type App } from "./api";
import CreateAppDialog from "./CreateAppDialog.vue";
import { homeTour } from "./tour";

/**
 * What a new person meets on the home of an app: the "Getting started" card (until it is done or hidden) and, the first time and once the page is quiet,
 * the tour of the home. The first-run wizard itself is the organisation page's (`OrgHome`); this is what carries on after it, in the app it made.
 */
const props = defineProps<{ app: App | undefined }>();
const me = useMe();
const account = useAccount();
const apps = useApps();
const creating = ref(false);

watch(() => me.data.value?.user?.id ?? "", loadProgress, { immediate: true });
const known = computed(() => !!me.data.value?.user?.id && !!apps.data.value && !!account.data.value);
const wizard = computed(() => !welcomeOff() && wizardOpen(progress.value, { appCount: known.value ? (apps.data.value?.apps ?? []).length : undefined, canCreate: account.data.value?.role !== "viewer" }));
const showChecklist = computed(() => !!progress.value && !progress.value.hidden && !wizard.value && known.value && !!props.app);
watch(
  () => known.value && !wizard.value && !!props.app,
  async (quiet) => {
    if (!quiet) return;
    await nextTick();
    startTourOnce(homeTour);
  },
  { immediate: true, flush: "post" },
);
</script>

<template>
  <GettingStarted v-if="showChecklist" :app="app" @create-app="creating = true" />
  <CreateAppDialog v-model:open="creating" />
</template>
