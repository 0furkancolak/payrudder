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
import { useAcceptTerms, useActivateLive, useApp, useGoLive } from "./api";
import { checklist, nextStep, progress, type GoLiveKey } from "./golive";

/**
 * The go-live checklist of an app's live environment, from the server (the same answer for every member; the server checks it again when the owner
 * activates). `compact` is the card on the home and on the live dashboard: the list and the count, with a link to the page that does the work. The full
 * version also has the owner's steps: the terms are accepted here, and the button switches live on. The other two things only a person can do are cards on the
 * same page (the billing profile and the payment provider); their items scroll to them.
 */
const props = defineProps<{ appId: string; compact?: boolean }>();
const { t } = useI18n();
const app = useApp(() => props.appId);
const live = computed(() => tenantOf(app.data.value?.app, "live"));
const goLive = useGoLive(() => props.appId, () => live.value !== "");
const activate = useActivateLive(() => props.appId);
const acceptTerms = useAcceptTerms(() => props.appId);
const accept = ref(false);

const items = computed(() => checklist(goLive.data.value));
const count = computed(() => progress(goLive.data.value));
const next = computed(() => nextStep(goLive.data.value));
const active = computed(() => goLive.data.value?.status === "TENANT_STATUS_ACTIVE");
const suspended = computed(() => goLive.data.value?.status === "TENANT_STATUS_SUSPENDED");
const isOwner = computed(() => app.data.value?.role === "owner");
const termsDone = computed(() => items.value.find((i) => i.key === "terms_accepted")?.done === true);
const allDone = computed(() => items.value.every((i) => i.done));
const canActivate = computed(() => isOwner.value && !active.value && !suspended.value && allDone.value);

/** Where the work of an item is done: a page, or a card further down this page (its id). */
function target(key: GoLiveKey): { to: string } | { anchor: string } | null {
  switch (key) {
    case "plan_published":
      return live.value ? { to: `/app/t/${live.value}/packages` } : null;
    case "legal_profile":
      return { anchor: "billing-profile" };
    case "payment_provider":
      return { to: `/app/apps/${props.appId}/providers` };
    case "terms_accepted":
      return { anchor: "go-live-terms-box" };
    default:
      return null;
  }
}

/** The link of an item, as a router link or null (the anchors are drawn as buttons that scroll). */
const pageTarget = (key: GoLiveKey): string | null => {
  const t = target(key);
  return t && "to" in t ? t.to : null;
};
function scrollTo(key: GoLiveKey) {
  const t = target(key);
  if (t && "anchor" in t) document.getElementById(t.anchor)?.scrollIntoView({ behavior: "smooth", block: "start" });
}

function go() {
  activate.mutate(false);
}
function acceptNow() {
  acceptTerms.mutate(undefined, { onSuccess: () => (accept.value = false) });
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
        </div>
        <Link v-if="!i.done && !compact && pageTarget(i.key)" :to="pageTarget(i.key)!" :search="(i.key === 'payment_provider' ? { env: 'live' } : undefined) as never" class="text-primary shrink-0 text-xs font-medium hover:underline" :data-testid="`go-live-go-${i.key}`">{{ t("product.goLive.open") }}</Link>
        <button v-else-if="!i.done && !compact && target(i.key)" type="button" class="text-primary shrink-0 text-xs font-medium hover:underline" :data-testid="`go-live-go-${i.key}`" @click="scrollTo(i.key)">{{ t("product.goLive.fillIn") }}</button>
      </li>
    </ol>

    <div v-if="compact && goLive.data.value && !active" class="flex flex-wrap items-center justify-between gap-3 border-t pt-4">
      <p class="text-muted-foreground text-xs">{{ t("product.goLive.nothingYet") }}</p>
      <Link :to="`/app/apps/${appId}/go-live`" :search="{ env: 'live' } as never" class="text-primary text-sm font-medium hover:underline" data-testid="go-live-open">{{ t("product.goLive.continue") }}</Link>
    </div>

    <div v-if="!compact && goLive.data.value && !active && !suspended" class="flex flex-col gap-3 border-t pt-4">
      <div id="go-live-terms-box" class="flex flex-col gap-2" data-testid="go-live-terms-box">
        <p v-if="termsDone" class="text-success text-sm" data-testid="go-live-terms-done">{{ t("product.goLive.termsAccepted") }}</p>
        <template v-else>
          <label class="flex items-start gap-3 text-sm">
            <Checkbox :model-value="accept" class="mt-0.5" :disabled="!isOwner" data-testid="go-live-terms" @update:model-value="accept = $event === true" />
            <span>{{ t("product.goLive.acceptTerms") }}</span>
          </label>
          <div class="flex flex-wrap items-center gap-3">
            <Button variant="outline" :disabled="!isOwner || !accept || acceptTerms.isPending.value" data-testid="go-live-accept-terms" @click="acceptNow">{{ t("product.goLive.acceptTermsButton") }}</Button>
          </div>
          <ErrorText :error="acceptTerms.error.value" />
        </template>
      </div>
      <p v-if="!isOwner" class="text-muted-foreground text-xs" data-testid="go-live-owner-only">{{ t("product.goLive.ownerOnly") }}</p>
      <div class="flex flex-wrap items-center gap-3">
        <Button :disabled="!canActivate || activate.isPending.value" data-testid="go-live-activate" @click="go"><Rocket />{{ t("product.goLive.activate") }}</Button>
        <p v-if="!allDone" class="text-muted-foreground text-xs">{{ t("product.goLive.notReady") }}</p>
      </div>
      <ErrorText :error="activate.error.value" />
    </div>
  </section>
</template>
