<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useAccount, useApp, useApps } from "@/features/product/api";
import { LIVE_PROVIDERS, type LiveProviderName } from "@/features/product/liveProvider";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useAppsProviders } from "./connectApi";
import { appRows, connectionOf, orgSummary, type ConnectionState, type Scope } from "./connections";
import ProviderSheet from "./ProviderSheet.vue";

/**
 * The payment providers of an app, or of the whole organisation (no app in the address). One card per provider: whether it is connected in live mode and
 * whether the provider accepts the keys now, with the sandbox noted as the built-in simulator. "Connect" opens a side sheet that asks for the keys, checks
 * them at the provider and can repeat the connection on every app of the organisation. On the organisation screen the apps are listed under the cards, and
 * each one can be set up differently from the rest.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const appId = computed(() => String((params.value as { appId?: string }).appId ?? ""));
const inApp = computed(() => appId.value !== "");

const apps = useApps();
const account = useAccount();
const app = useApp(appId);
const rows = computed(() => appRows(apps.data.value?.apps));
const overview = useAppsProviders(rows);
const isOwner = computed(() => (inApp.value ? app.data.value?.role === "owner" : account.data.value?.role === "owner"));
const loading = computed(() => apps.isPending.value || (inApp.value && app.isPending.value));
const liveApps = computed(() => rows.value.filter((r) => r.hasLive));
/** The organisation-wide choice is for an owner of an organisation with two or more apps that can take a live account. */
const canOrg = computed(() => isOwner.value && liveApps.value.length > 1);
const appName = computed(() => rows.value.find((r) => r.id === appId.value)?.name ?? "");
const hasLive = computed(() => (inApp.value ? (rows.value.find((r) => r.id === appId.value)?.hasLive ?? false) : liveApps.value.length > 0));

const names = (p: string) => t(`product.goLive.provider.names.${p}`);
const tone = (s: ConnectionState) => (s === "working" ? "success" : s === "problem" ? "danger" : "neutral");
const stateLabel = (s: ConnectionState) => t(`product.providers.state.${s}`);

/** What a card says about the live environment. */
function liveLine(p: LiveProviderName): { state: ConnectionState; text: string } {
  if (inApp.value) {
    const c = connectionOf(overview.value.find((r) => r.app.id === appId.value)?.accounts, p);
    const text = c.problem ? t(`product.goLive.provider.accountProblems.${c.problem}`) : c.state === "working" ? (c.isDefault ? t("product.providers.defaultAccount") : t("product.providers.connectedLine")) : t("product.providers.notConnectedLine");
    return { state: c.state, text };
  }
  const s = orgSummary(overview.value, p);
  const text = s.apps === 0 ? t("product.providers.noLiveApps") : s.connected === 0 ? t("product.providers.notConnectedLine") : t("product.providers.orgLine", { connected: s.connected, total: s.apps });
  return { state: s.state, text };
}

const sheetOpen = ref(false);
const sheetProvider = ref<LiveProviderName | "">("");
const sheetApp = ref("");
const sheetScope = ref<Scope>("app");
const notice = ref("");

function openSheet(p: LiveProviderName, forApp: string, scope: Scope) {
  sheetProvider.value = p;
  sheetApp.value = forApp;
  sheetScope.value = scope;
  notice.value = "";
  sheetOpen.value = true;
}
/** From a card: on an app's screen the app, on the organisation's screen every app (an owner with several) or the only app. */
function fromCard(p: LiveProviderName) {
  if (inApp.value) return openSheet(p, appId.value, "app");
  const single = liveApps.value.length === 1 ? (liveApps.value[0]?.id ?? "") : "";
  openSheet(p, single, canOrg.value ? "org" : "app");
}
function onDone(s: { provider: LiveProviderName; scope: Scope; ok: number; failed: number }) {
  notice.value = s.failed === 0 ? t("product.providers.doneAll", { provider: names(s.provider), count: s.ok }) : t("product.providers.donePartial", { provider: names(s.provider), ok: s.ok, failed: s.failed });
}

const initial = (p: string) => names(p).charAt(0).toUpperCase();
const LOGO: Record<LiveProviderName, string> = {
  iyzico: "bg-sky-100 text-sky-700 dark:bg-sky-950 dark:text-sky-300",
  paddle: "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300",
  banktransfer: "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300",
};
</script>

<template>
  <section class="flex flex-col gap-6" data-testid="providers-screen">
    <header class="px-1">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("shell.items.providers") }}</h1>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ inApp ? t("product.providers.introApp", { app: appName }) : t("product.providers.introOrg") }}</p>
    </header>

    <Skeleton v-if="loading" class="h-48 rounded-2xl" />
    <ErrorText :error="apps.error.value" />
    <p v-if="!loading && !hasLive" class="text-muted-foreground surface p-6 text-sm" data-testid="providers-no-live">{{ t("product.goLive.noLive") }}</p>
    <template v-else-if="!loading">
      <p v-if="notice" class="text-success text-sm" role="status" data-testid="provider-connected">{{ notice }}</p>
      <p v-if="!isOwner" class="text-muted-foreground text-sm" data-testid="providers-owner-only">{{ t("product.goLive.provider.ownerOnly") }}</p>

      <ul class="grid gap-4 lg:grid-cols-3" :aria-label="t('shell.items.providers')">
        <li v-for="p in LIVE_PROVIDERS" :key="p" class="surface flex flex-col gap-4 p-5" :data-testid="`provider-card-${p}`" :data-state="liveLine(p).state">
          <div class="flex items-center gap-3">
            <span class="flex size-11 shrink-0 items-center justify-center rounded-2xl text-lg font-semibold" :class="LOGO[p]" aria-hidden="true">{{ initial(p) }}</span>
            <div class="min-w-0">
              <h2 class="text-base font-semibold tracking-tight">{{ names(p) }}</h2>
              <p class="text-muted-foreground text-xs">{{ t(`product.providers.about.${p}`) }}</p>
            </div>
          </div>
          <div class="grid gap-2 text-sm">
            <div class="flex items-center justify-between gap-3">
              <span class="text-muted-foreground">{{ t("product.env.live") }}</span>
              <span><StatusPill :tone="tone(liveLine(p).state)" :data-testid="`provider-state-${p}`">{{ stateLabel(liveLine(p).state) }}</StatusPill></span>
            </div>
            <p class="text-muted-foreground -mt-1 text-xs" :class="liveLine(p).state === 'problem' ? 'text-destructive' : ''">{{ liveLine(p).text }}</p>
            <div class="flex items-center justify-between gap-3">
              <span class="text-muted-foreground">{{ t("product.env.sandbox") }}</span>
              <span><StatusPill tone="info">{{ t("product.providers.simulator") }}</StatusPill></span>
            </div>
          </div>
          <div class="mt-auto flex flex-wrap gap-2">
            <Button :variant="liveLine(p).state === 'none' ? 'default' : 'outline'" :disabled="!isOwner" :data-testid="`provider-open-${p}`" @click="fromCard(p)">
              {{ liveLine(p).state === "none" ? t("product.providers.connect") : t("product.providers.manage") }}
            </Button>
          </div>
        </li>
      </ul>
      <p class="text-muted-foreground px-1 text-xs">{{ t("product.providers.sandboxNote") }}</p>

      <div v-if="!inApp && rows.length > 0" class="surface flex flex-col gap-3 p-5" data-testid="providers-apps">
        <div>
          <h2 class="text-base font-semibold tracking-tight">{{ t("product.providers.appsTitle") }}</h2>
          <p class="text-muted-foreground text-sm">{{ t("product.providers.appsIntro") }}</p>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="text-muted-foreground border-b text-start text-xs">
                <th class="py-2 pe-3 text-start font-medium">{{ t("product.providers.app") }}</th>
                <th v-for="p in LIVE_PROVIDERS" :key="p" class="px-3 py-2 text-start font-medium">{{ names(p) }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in overview" :key="r.app.id" class="border-b last:border-0" :data-testid="`providers-app-${r.app.id}`">
                <th scope="row" class="py-2.5 pe-3 text-start font-medium">
                  <Link :to="`/app/apps/${r.app.id}/providers`" :search="{ env: 'live', app: r.app.id } as never" class="hover:underline">{{ r.app.name }}</Link>
                </th>
                <td v-for="p in LIVE_PROVIDERS" :key="p" class="px-3 py-2.5">
                  <span v-if="!r.app.hasLive" class="text-muted-foreground text-xs">{{ t("product.providers.noLive") }}</span>
                  <button v-else type="button" class="rounded-full focus-visible:ring-ring/50 focus-visible:ring-[3px] focus-visible:outline-none disabled:cursor-default" :disabled="!isOwner" :data-testid="`providers-cell-${r.app.id}-${p}`" :aria-label="`${r.app.name}: ${names(p)}`" @click="openSheet(p, r.app.id, 'app')">
                    <StatusPill :tone="tone(connectionOf(r.accounts, p).state)">{{ stateLabel(connectionOf(r.accounts, p).state) }}</StatusPill>
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>

    <ProviderSheet v-model:open="sheetOpen" :provider="sheetProvider" :app-id="sheetApp" :apps="rows" :scope="sheetScope" :can-org="canOrg" :is-owner="isOwner" @done="onDone" />
  </section>
</template>
