<script setup lang="ts">
import { Link, useRouter } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { isPerson } from "@/shared/lib/session";
import { Button } from "@/shared/ui/button";
import { ErrorState } from "@/shared/ui/error-state";

/**
 * What a route shows when its page threw while loading or drawing (the router's `defaultErrorComponent`, and the page error boundary's own state): the
 * failure as a state with a way to try again and a way home. An error from the API keeps its translated message and request id; anything else (a bug in
 * the page) says only that this page could not be shown, never a stack trace.
 */
const props = defineProps<{ error?: unknown; reset?: () => void }>();
const { t } = useI18n();
const router = useRouter();
const fromApi = computed(() => props.error !== undefined && props.error !== null && toApiError(props.error).code !== "ERROR");
const home = computed(() => (isPerson.value ? "/app" : "/signin"));

function retry() {
  props.reset?.();
  void router.invalidate();
}
</script>

<template>
  <div class="mx-auto w-full max-w-xl py-10" data-testid="route-error">
    <ErrorState :error="fromApi ? error : undefined" :title="fromApi ? undefined : t('errorState.pageTitle')" :message="fromApi ? undefined : t('errorState.pageBody')" @retry="retry">
      <template #actions>
        <Button as-child size="sm" variant="ghost"><Link :to="home">{{ t("errorState.home") }}</Link></Button>
      </template>
    </ErrorState>
  </div>
</template>
