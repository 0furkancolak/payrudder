<script setup lang="ts">
import { AlertTriangle, Copy, Loader2, RefreshCw } from "@lucide/vue";
import { computed, type HTMLAttributes } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { copyText } from "@/shared/lib/clipboard";
import { useErrorText } from "@/shared/lib/errors";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/button";
import { toast } from "vue-sonner";

/**
 * A failure as a state of the page, not a line of red text: a title for the kind of failure (not found, no access, signed out, the server could not be
 * reached, anything else), the message in the reader's language (the API's stable error key translated, the server's English text only as the fallback), a
 * button to try again and the request id the gateway gave, with a button to copy it, so a report to support names the exact request. For a page or a card
 * whose data did not arrive (`error` is what the query threw); a failure of a form's own action stays a line of `ErrorText`.
 *
 * It draws nothing while there is no `error` (and no `title` or `message`), so a page can put it in place without a condition.
 *
 * `title` and `message` replace the derived ones (the route-level states have their own words); `compact` is the smaller block inside a table card.
 */
const props = defineProps<{ error?: unknown; title?: string; message?: string; requestId?: string; retrying?: boolean; compact?: boolean; class?: HTMLAttributes["class"] }>();
const emit = defineEmits<{ retry: [] }>();
const { t } = useI18n();
const errorText = useErrorText();

const api = computed(() => (props.error === undefined || props.error === null ? undefined : toApiError(props.error)));
const kind = computed<"notFound" | "forbidden" | "unauthenticated" | "network" | "generic">(() => {
  const e = api.value;
  if (!e) return "generic";
  if (e.code === "NOT_FOUND") return "notFound";
  if (e.code === "PERMISSION_DENIED") return "forbidden";
  if (e.code === "UNAUTHENTICATED") return "unauthenticated";
  // An error with no envelope is a request that never got an answer (offline, a dropped connection, a blocked call).
  if (e.code === "ERROR" && e.key === "" && e.requestId === "") return "network";
  return "generic";
});
const heading = computed(() => props.title ?? t(`errorState.title.${kind.value}`));
const body = computed(() => props.message ?? (api.value ? errorText(props.error) : t("errorState.fallback")));
const id = computed(() => props.requestId ?? api.value?.requestId ?? "");

async function copyId() {
  if (await copyText(id.value)) toast.success(t("common.copied"));
  else toast.error(t("common.copyFailed"));
}
</script>

<template>
  <section v-if="error || title || message" role="alert" :class="cn('bg-destructive-soft text-foreground flex items-start gap-3 rounded-2xl', compact ? 'mx-3 mb-3 px-4 py-3' : 'px-5 py-5', props.class)" data-testid="error-state">
    <AlertTriangle class="text-destructive mt-0.5 size-5 shrink-0" aria-hidden="true" />
    <div class="min-w-0 flex-1">
      <h2 :class="compact ? 'text-sm font-semibold' : 'text-base font-semibold'" data-testid="error-title">{{ heading }}</h2>
      <p class="text-muted-foreground mt-1 text-sm break-words" data-testid="error-message">{{ body }}</p>
      <div class="mt-3 flex flex-wrap items-center gap-2">
        <Button type="button" size="sm" variant="outline" :disabled="retrying" data-testid="error-retry" @click="emit('retry')">
          <Loader2 v-if="retrying" class="size-4 animate-spin" aria-hidden="true" /><RefreshCw v-else class="size-4" aria-hidden="true" />{{ retrying ? t("errorState.retrying") : t("errorState.retry") }}
        </Button>
        <slot name="actions" />
        <button v-if="id" type="button" class="text-muted-foreground hover:text-foreground inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-xs" :aria-label="t('errorState.copyRequestId')" data-testid="error-request-id" @click="copyId">
          <span>{{ t("errorState.requestId") }}</span><code class="mono">{{ id }}</code><Copy class="size-3.5" aria-hidden="true" />
        </button>
      </div>
    </div>
  </section>
</template>
