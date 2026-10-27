<script setup lang="ts">
import { Link, useNavigate, useSearch } from "@tanstack/vue-router";
import { Plus, Rocket } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import OrgDashboard from "@/features/dashboard/OrgDashboard.vue";
import { parseQuery, toSearch, type DashQuery } from "@/features/dashboard/lib";
import { useShell } from "@/app/layout/useShell";
import { ALL_APPS } from "@/shared/lib/scope";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Skeleton } from "@/shared/ui/skeleton";
import { useAccount, useApps, useMe } from "./api";
import AccountSwitcher from "./AccountSwitcher.vue";
import AppCard from "./AppCard.vue";
import AppGoLiveCard from "./AppGoLiveCard.vue";
import CreateAppDialog from "./CreateAppDialog.vue";
import { hasPartnerRole, ownMemberships } from "./lib";
import { defaultOrgEnv, isEnvironment, liveState, tenantOf, type Environment } from "@/shared/lib/environment";
import type { App } from "./api";

const { t } = useI18n();
const shell = useShell();
const me = useMe();
const account = useAccount();
const apps = useApps();
const creating = ref(false);
const search = useSearch({ strict: false });
const navigate = useNavigate();
const query = computed(() => parseQuery(search.value as Record<string, unknown>));
const list = computed(() => apps.data.value?.apps ?? []);
/** The address's environment, else live when some app is active there, else the sandbox. Decided once the apps have arrived, so the home never flips. */
const env = computed<Environment | null>(() => {
  const v = (search.value as { env?: string }).env;
  if (isEnvironment(v)) return v;
  return apps.data.value ? defaultOrgEnv(list.value) : null;
});

/** The dashboard keeps its period, currency and environment in the address, like an app's dashboard does. */
function go(q: DashQuery, e: Environment) {
  void navigate({ to: "/app", search: { app: ALL_APPS, env: e, ...toSearch(q) } as never, replace: true });
}

/** Apps with something to add up in this environment: every app in the sandbox, and in live the ones whose live environment is active. */
const withFigures = computed(() => list.value.filter((a) => env.value !== null && tenantOf(a, env.value) !== "" && (env.value === "sandbox" || liveState(a) === "active")));
/** On the live view: apps whose live environment is not active show their checklist, not zeros. */
const notLive = (a: App) => env.value === "live" && liveState(a) !== "active";
const canCreate = computed(() => account.data.value?.role !== "viewer");
/** A person who left (or was removed from) every account has nothing to show; the page says so instead of listing errors. */
// A membership with the role affiliate is a seller's account the person is a partner of: it is not an account of theirs.
const noAccount = computed(() => me.data.value?.user !== undefined && ownMemberships(me.data.value.memberships).length === 0);
const partner = computed(() => hasPartnerRole(me.data.value?.memberships));
const first = computed(() => (me.data.value?.user?.display_name || me.data.value?.user?.email?.split("@")[0] || "").split(/\s+/)[0]);

// An app is always selected: this page is the view of every app together (`?app=all`); without that choice the person lands in the app used last (else the first).
const enteringApp = computed(() => {
  const s = shell.scope.value;
  // Somebody without an account of their own has nothing to be inside of: the page explains that instead.
  // Only while the address is this page's own: a link to an app's page changes the scope before this page is gone, and must not be turned back.
  return shell.path.value === "/app" && !noAccount.value && s && s !== ALL_APPS ? s : "";
});
watch(
  [enteringApp, env],
  ([appId, e]) => {
    if (!appId || !e) return;
    const { sheet } = search.value as { sheet?: string };
    void navigate({ to: "/app/apps/$appId", params: { appId }, search: { app: appId, env: e, ...toSearch(query.value), ...(sheet ? { sheet } : {}) } as never, replace: true });
  },
  { immediate: true },
);
</script>

<template>
  <section v-if="noAccount" class="surface mx-auto flex max-w-xl flex-col items-center gap-3 px-6 py-16 text-center" data-testid="no-account">
    <h1 class="text-xl font-semibold tracking-tight">{{ t("product.account.noneTitle") }}</h1>
    <p class="text-muted-foreground text-sm">{{ t("product.account.noneBody") }}</p>
    <Link v-if="partner" to="/app/partner" class="text-brand-strong text-sm font-medium underline-offset-4 hover:underline" data-testid="to-partner">{{ t("shell.items.partner") }}</Link>
  </section>
  <section v-else class="flex flex-col gap-8">
    <header class="flex flex-wrap items-end justify-between gap-4 px-1">
      <div>
        <p class="text-muted-foreground text-sm">{{ account.data.value?.name ?? "" }}</p>
        <AccountSwitcher class="mb-1 block" />
        <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ first ? t("product.home.welcome", { name: first }) : t("product.home.title") }}</h1>
        <p class="text-muted-foreground mt-1 text-sm">{{ t("product.home.intro") }}</p>
      </div>
      <Button v-if="canCreate" size="lg" @click="creating = true"><Plus />{{ t("product.home.create") }}</Button>
    </header>

    <ErrorText :error="apps.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />

    <div v-if="apps.isPending.value" class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3" aria-busy="true">
      <Skeleton v-for="n in 3" :key="n" class="h-52 rounded-3xl" />
    </div>

    <OrgDashboard v-else-if="env && !enteringApp && withFigures.length > 0" :apps="list" :query="query" :env="env" @update:query="go($event, env)" />

    <section v-if="env && list.length > 0" class="flex flex-col gap-3" :aria-label="t('shell.items.overview')">
      <h2 class="px-1 text-xl font-semibold tracking-tight">{{ t("shell.items.overview") }}</h2>
      <div class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3" :data-env="env">
      <template v-for="app in list" :key="app.id">
        <AppGoLiveCard v-if="notLive(app)" :app="app" />
        <AppCard v-else :app="app" :env="env" />
      </template>
      <button
        v-if="canCreate"
        type="button"
        class="border-border text-muted-foreground hover:border-brand hover:text-foreground flex min-h-52 flex-col items-center justify-center gap-3 rounded-3xl border-2 border-dashed p-6 transition-colors"
        @click="creating = true"
      >
        <span class="bg-brand-soft text-brand-strong flex size-12 items-center justify-center rounded-2xl"><Plus class="size-6" /></span>
        <span class="text-sm font-medium">{{ t("product.home.create") }}</span>
      </button>
      </div>
    </section>

    <div v-else-if="!apps.error.value && !apps.isPending.value" class="surface flex flex-col items-center gap-4 px-6 py-16 text-center">
      <span class="bg-brand-soft text-brand-strong flex size-16 items-center justify-center rounded-3xl"><Rocket class="size-8" /></span>
      <div>
        <h2 class="text-xl font-semibold tracking-tight">{{ t("product.home.emptyTitle") }}</h2>
        <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">{{ t("product.home.emptyBody") }}</p>
      </div>
      <Button v-if="canCreate" size="lg" @click="creating = true"><Plus />{{ t("product.home.create") }}</Button>
      <p v-else class="text-muted-foreground text-sm">{{ t("product.home.viewerEmpty") }}</p>
    </div>

    <CreateAppDialog v-model:open="creating" />
  </section>
</template>
