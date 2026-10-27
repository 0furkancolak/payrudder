<script setup lang="ts">
import { useRouterState } from "@tanstack/vue-router";
import { onErrorCaptured, ref, watch } from "vue";
import RouteError from "./RouteError.vue";

/**
 * The error boundary around the pages: whatever a page throws while it renders or in one of its handlers is caught here and shown as the failure state
 * instead of a blank screen, with the frame (sidebar, header) still working. Trying again draws the page anew; going to another page clears it.
 */
const failure = ref<{ error: unknown } | null>(null);
const path = useRouterState({ select: (s) => s.location.pathname });
const attempt = ref(0);

onErrorCaptured((error) => {
  failure.value = { error };
  console.error(error);
  return false;
});
watch(path, () => (failure.value = null));
function reset() {
  failure.value = null;
  attempt.value++;
}
</script>

<template>
  <RouteError v-if="failure" :error="failure.error" :reset="reset" />
  <div v-else :key="attempt" class="contents"><slot /></div>
</template>
