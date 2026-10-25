<script setup lang="ts">
import { useNavigate, useSearch } from "@tanstack/vue-router";
import { Plus, Rocket } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import OrgDashboard from "@/features/dashboard/OrgDashboard.vue";
import { parseQuery, toSearch, type DashQuery } from "@/features/dashboard/lib";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Skeleton } from "@/shared/ui/skeleton";
import { useAccount, useApps, useMe } from "./api";
import AccountSwitcher from "./AccountSwitcher.vue";
import AppCard from "./AppCard.vue";
import CreateAppDialog from "./CreateAppDialog.vue";
import type { Environment } from "@/shared/lib/environment";

const { t } = useI18n();
const errorText = useErrorText();
const me = useMe();
const account = useAccount();
const apps = useApps();
const creating = ref(false);
const search = useSearch({ strict: false });
const navigate = useNavigate();
const query = computed(() => parseQuery(search.value as Record<string, unknown>));
const env = computed<Environment>(() => ((search.value as { env?: string }).env === "sandbox" ? "sandbox" : "live"));

/** The dashboard keeps its period, currency and environment in the address, like an app's dashboard does. */
function go(q: DashQuery, e: Environment) {
  void navigate({ to: "/app", search: { ...(e === "sandbox" ? { env: "sandbox" } : {}), ...toSearch(q) } as never, replace: true });
}

const list = computed(() => apps.data.value?.apps ?? []);
const canCreate = computed(() => account.data.value?.role !== "viewer");
/** A person who left (or was removed from) every account has nothing to show; the page says so instead of listing errors. */
const noAccount = computed(() => me.data.value?.user !== undefined && (me.data.value.memberships ?? []).length === 0);
const first = computed(() => (me.data.value?.user?.display_name || me.data.value?.user?.email?.split("@")[0] || "").split(/\s+/)[0]);
</script>

<template>
  <section v-if="noAccount" class="surface mx-auto flex max-w-xl flex-col items-center gap-3 px-6 py-16 text-center" data-testid="no-account">
    <h1 class="text-xl font-semibold tracking-tight">{{ t("product.account.noneTitle") }}</h1>
    <p class="text-muted-foreground text-sm">{{ t("product.account.noneBody") }}</p>
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

    <p v-if="apps.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(apps.error.value) }}</p>

    <div v-if="apps.isPending.value" class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3" aria-busy="true">
      <Skeleton v-for="n in 3" :key="n" class="h-52 rounded-3xl" />
    </div>

    <OrgDashboard v-else-if="list.length > 0" :apps="list" :query="query" :env="env" @update:query="go($event, env)" @update:env="go({ ...query, cur: undefined }, $event)" />

    <div v-if="list.length > 0" class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
      <AppCard v-for="app in list" :key="app.id" :app="app" />
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

    <div v-else-if="!apps.error.value" class="surface flex flex-col items-center gap-4 px-6 py-16 text-center">
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
