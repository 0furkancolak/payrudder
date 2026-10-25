<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { Check, Circle, Rocket } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { tenantOf } from "@/shared/lib/environment";
import { Button } from "@/shared/ui/button";
import { Checkbox } from "@/shared/ui/checkbox";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { isOperator } from "@/shared/lib/session";
import { useActivateLive, useApp, useGoLive } from "./api";
import { checklist, nextStep, progress, type GoLiveKey } from "./golive";

/**
 * The go-live checklist of an app's live environment, from the server (the same answer for every member; the server checks it again when the owner
 * activates). `compact` is the card on the home and on the live dashboard: the list and the count, with a link to the page that does the work. The full
 * version also has the button, which only an owner sees. Two of the items (the seller's legal profile and the payment provider) are set up by the
 * platform team for now: an operator gets the link to the page, everybody else is told who to ask.
 */
const props = defineProps<{ appId: string; compact?: boolean }>();
const { t } = useI18n();
const app = useApp(() => props.appId);
const live = computed(() => tenantOf(app.data.value?.app, "live"));
const goLive = useGoLive(() => props.appId, () => live.value !== "");
const activate = useActivateLive(() => props.appId);
const accept = ref(false);

const items = computed(() => checklist(goLive.data.value));
const count = computed(() => progress(goLive.data.value));
const next = computed(() => nextStep(goLive.data.value));
const active = computed(() => goLive.data.value?.status === "TENANT_STATUS_ACTIVE");
const suspended = computed(() => goLive.data.value?.status === "TENANT_STATUS_SUSPENDED");
const isOwner = computed(() => app.data.value?.role === "owner");
/** Everything but the terms is done: the terms are accepted in the same click as the activation. */
const readyForTerms = computed(() => items.value.every((i) => i.done || i.key === "terms_accepted"));
const termsDone = computed(() => items.value.find((i) => i.key === "terms_accepted")?.done === true);
const canActivate = computed(() => isOwner.value && !active.value && !suspended.value && readyForTerms.value && (termsDone.value || accept.value));

/** Where the work of an item is done, or null when the person cannot do it themselves. */
function target(key: GoLiveKey): { to: string; search?: Record<string, string> } | null {
  switch (key) {
    case "plan_published":
      return live.value ? { to: `/app/t/${live.value}/packages` } : null;
    case "legal_profile":
      return isOperator.value && live.value ? { to: `/tenants/${live.value}/address` } : null;
    case "payment_provider":
      return isOperator.value && live.value ? { to: `/tenants/${live.value}/providers` } : null;
    default:
      return null;
  }
}

function go() {
  activate.mutate(termsDone.value || accept.value);
}
</script>

<template>
  <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.goLive.title')" data-testid="go-live">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex items-start gap-3">
        <span class="bg-brand-soft text-brand-strong flex size-10 shrink-0 items-center justify-center rounded-2xl" aria-hidden="true"><Rocket class="size-5" /></span>
        <div>
          <h2 class="text-lg font-semibold tracking-tight">{{ t("product.goLive.title") }}</h2>
          <p class="text-muted-foreground text-sm">{{ active ? t("product.goLive.liveNow") : suspended ? t("product.goLive.suspended") : t("product.goLive.intro") }}</p>
        </div>
      </div>
      <StatusPill v-if="goLive.data.value" :tone="active ? 'success' : suspended ? 'danger' : 'warning'" data-testid="go-live-state">
        {{ active ? t("product.goLive.stateActive") : suspended ? t("product.goLive.stateSuspended") : t("product.goLive.count", { done: count.done, total: count.total }) }}
      </StatusPill>
    </div>

    <Skeleton v-if="goLive.isPending.value && live" class="h-32 rounded-2xl" />
    <p v-else-if="!live" class="text-muted-foreground text-sm" data-testid="go-live-none">{{ t("product.goLive.noLive") }}</p>
    <ErrorText :error="goLive.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />

    <ol v-if="goLive.data.value" class="flex flex-col gap-2" :aria-label="t('product.goLive.title')">
      <li v-for="i in items" :key="i.key" class="flex items-start gap-3 rounded-2xl px-3 py-2" :class="i.key === next && !compact ? 'bg-muted' : ''" :data-testid="`go-live-${i.key}`" :data-done="i.done">
        <span class="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full" :class="i.done ? 'bg-success-soft text-success' : 'text-muted-foreground'" aria-hidden="true">
          <Check v-if="i.done" class="size-3.5" /><Circle v-else class="size-4" />
        </span>
        <div class="min-w-0 flex-1">
          <p class="text-sm font-medium">
            {{ t(`product.goLive.items.${i.key}`) }}<span class="sr-only"> - {{ i.done ? t("product.goLive.done") : t("product.goLive.todo") }}</span>
          </p>
          <p v-if="!i.done && !compact" class="text-muted-foreground text-xs">{{ t(`product.goLive.help.${i.key}`) }}</p>
          <p v-if="!i.done && !compact && (i.key === 'legal_profile' || i.key === 'payment_provider') && !target(i.key)" class="text-muted-foreground text-xs">{{ t("product.goLive.askTeam") }}</p>
        </div>
        <Link v-if="!i.done && !compact && target(i.key)" :to="target(i.key)!.to" class="text-primary shrink-0 text-xs font-medium hover:underline">{{ t("product.goLive.open") }}</Link>
      </li>
    </ol>

    <div v-if="compact && goLive.data.value && !active" class="flex flex-wrap items-center justify-between gap-3 border-t pt-4">
      <p class="text-muted-foreground text-xs">{{ t("product.goLive.nothingYet") }}</p>
      <Link :to="`/app/apps/${appId}/go-live`" :search="{ env: 'live' } as never" class="text-primary text-sm font-medium hover:underline" data-testid="go-live-open">{{ t("product.goLive.continue") }}</Link>
    </div>

    <div v-if="!compact && goLive.data.value && !active && !suspended" class="flex flex-col gap-3 border-t pt-4">
      <label v-if="!termsDone" class="flex items-start gap-3 text-sm">
        <Checkbox :model-value="accept" class="mt-0.5" :disabled="!isOwner" data-testid="go-live-terms" @update:model-value="accept = $event === true" />
        <span>{{ t("product.goLive.acceptTerms") }}</span>
      </label>
      <p v-if="!isOwner" class="text-muted-foreground text-xs" data-testid="go-live-owner-only">{{ t("product.goLive.ownerOnly") }}</p>
      <div class="flex flex-wrap items-center gap-3">
        <Button :disabled="!canActivate || activate.isPending.value" data-testid="go-live-activate" @click="go"><Rocket />{{ t("product.goLive.activate") }}</Button>
        <p v-if="!readyForTerms" class="text-muted-foreground text-xs">{{ t("product.goLive.notReady") }}</p>
      </div>
      <ErrorText :error="activate.error.value" />
    </div>
  </section>
</template>
