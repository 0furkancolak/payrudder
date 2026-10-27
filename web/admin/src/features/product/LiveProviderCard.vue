<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { CreditCard } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { appRows } from "@/features/providers/connections";
import ProviderSheet from "@/features/providers/ProviderSheet.vue";
import { useSheetTarget } from "@/shared/lib/useSheetTarget";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useApp, useApps, useLiveProviderActions, useLiveProviders } from "./api";
import { LIVE_PROVIDERS, isLiveProvider, problemCode, type LiveProviderName } from "./liveProvider";

/**
 * Connecting a payment provider to the live environment: the "payment provider" item of the go-live checklist. Only the owner connects or removes one. The keys
 * are typed in the same side sheet as on the payment providers screen; the server asks the provider to accept them before it stores them (sealed), and they are
 * never shown again. The list below shows each live account, whether the provider accepts it now, and why not when it does not.
 */
const props = defineProps<{ appId: string }>();
const { t } = useI18n();
const app = useApp(() => props.appId);
const apps = useApps();
const live = computed(() => app.data.value?.app?.tenants?.find((x) => x.environment === "ENVIRONMENT_LIVE"));
const isOwner = computed(() => app.data.value?.role === "owner");
const providers = useLiveProviders(() => props.appId, () => !!live.value);
const { remove } = useLiveProviderActions(() => props.appId);

const accounts = computed(() => providers.data.value?.accounts ?? []);
const rows = computed(() => appRows(apps.data.value?.apps));
const sheetOpen = ref(false);
const sheetProvider = ref<LiveProviderName | "">("");
const notice = ref("");

const providerName = (p: string) => (isLiveProvider(p) ? t(`product.goLive.provider.names.${p}`) : p);

function openSheet(p: LiveProviderName) {
  notice.value = "";
  sheetProvider.value = p;
  sheetOpen.value = true;
}
// The provider being connected is in the address (`connect-provider:stripe`), so a reload brings the sheet back once the apps have loaded.
const providerTarget = computed<LiveProviderName | null>({ get: () => sheetProvider.value || null, set: (v) => (sheetProvider.value = v ?? "") });
useSheetTarget("connect-provider", sheetOpen, providerTarget, { id: (p) => p, find: (id) => (rows.value.length > 0 && isLiveProvider(id) ? id : undefined) });
function onDone(s: { provider: LiveProviderName; ok: number; failed: number }) {
  notice.value = s.failed === 0 ? t("product.goLive.provider.connectedNow", { provider: providerName(s.provider) }) : "";
}
function removeAccount(p: string) {
  if (isLiveProvider(p) && window.confirm(t("product.goLive.provider.confirmRemove", { provider: providerName(p) }))) remove.mutate(p);
}
</script>

<template>
  <section id="live-provider" class="surface flex flex-col gap-4 p-6" :aria-label="t('product.goLive.provider.title')" data-testid="live-provider">
    <div class="flex items-start gap-3">
      <span class="bg-brand-soft text-brand-strong flex size-10 shrink-0 items-center justify-center rounded-2xl" aria-hidden="true"><CreditCard class="size-5" /></span>
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.goLive.provider.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("product.goLive.provider.intro") }}</p>
      </div>
    </div>

    <Skeleton v-if="app.isPending.value || providers.isPending.value" class="h-24 rounded-2xl" />
    <p v-else-if="!live" class="text-muted-foreground text-sm">{{ t("product.goLive.noLive") }}</p>
    <template v-else>
      <ErrorText :error="providers.error.value" />
      <ul v-if="accounts.length > 0" class="flex flex-col gap-2" :aria-label="t('product.goLive.provider.connected')" data-testid="live-accounts">
        <li v-for="a in accounts" :key="a.provider" class="bg-muted flex flex-wrap items-center justify-between gap-3 rounded-2xl px-4 py-3" :data-testid="`live-account-${a.provider}`" :data-working="a.working">
          <div class="min-w-0">
            <p class="text-sm font-medium">
              {{ providerName(a.provider ?? "") }}
              <span v-if="a.default" class="text-muted-foreground text-xs font-normal">- {{ t("product.goLive.provider.default") }}</span>
            </p>
            <p v-if="problemCode(a.problem)" class="text-destructive text-xs" data-testid="live-account-problem">{{ t(`product.goLive.provider.accountProblems.${problemCode(a.problem)}`) }}</p>
            <p v-else class="text-muted-foreground text-xs">{{ a.checked ? t("product.goLive.provider.checkedYes") : t("product.goLive.provider.checkedNo") }}</p>
          </div>
          <div class="flex items-center gap-3">
            <StatusPill :tone="a.working ? 'success' : 'danger'">{{ a.working ? t("product.goLive.provider.working") : t("product.goLive.provider.notWorking") }}</StatusPill>
            <Button v-if="isOwner" variant="ghost" size="sm" :disabled="remove.isPending.value" :data-testid="`live-account-remove-${a.provider}`" @click="removeAccount(a.provider ?? '')">{{ t("product.goLive.provider.remove") }}</Button>
          </div>
        </li>
      </ul>
      <p v-else class="text-muted-foreground text-sm" data-testid="live-accounts-none">{{ t("product.goLive.provider.none") }}</p>
      <p v-if="notice" class="text-success text-sm" role="status" data-testid="provider-connected">{{ notice }}</p>
      <ErrorText :error="remove.error.value" />

      <p v-if="!isOwner" class="text-muted-foreground text-xs" data-testid="provider-owner-only">{{ t("product.goLive.provider.ownerOnly") }}</p>
      <div v-else class="flex flex-wrap items-center gap-2 border-t pt-4">
        <span class="text-sm font-medium">{{ t("product.goLive.provider.connectTitle") }}</span>
        <Button v-for="p in LIVE_PROVIDERS" :key="p" variant="outline" size="sm" :data-testid="`provider-open-${p}`" @click="openSheet(p)">{{ providerName(p) }}</Button>
        <Link :to="`/app/apps/${appId}/integrations`" :search="{ env: 'live' } as never" class="text-primary ms-auto text-sm font-medium hover:underline" data-testid="provider-screen-link">{{ t("product.providers.openScreen") }}</Link>
      </div>
      <ProviderSheet v-model:open="sheetOpen" :provider="sheetProvider" :app-id="appId" :apps="rows" scope="app" :can-org="false" :is-owner="isOwner" @done="onDone" />
    </template>
  </section>
</template>
