<script setup lang="ts">
import { Link, useNavigate, useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { formatDuration } from "@/shared/lib/duration";
import { envOf } from "@/shared/lib/environment";
import { currencyInfo, formatOffset, zoneMinutes } from "@/shared/lib/intl";
import { enumWord, label } from "@/shared/lib/labels";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { PageHeader } from "@/shared/ui/page-header";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useApp, useDeleteApp, useRenameApp } from "./api";
import { canDeleteApp, canRenameApp, confirmsDelete, nameToSave } from "./appManage";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";

/**
 * What an app was created with: its name and address, and for each environment its currency, time zone, grace period and plan. The currency and the
 * time zone are fixed when the app is created (they decide how every invoice is worked out), so this page shows them and does not offer to change them.
 * Packages and prices in other currencies are added per package, not here. Owners and admins rename the app here; an owner can also delete it, which archives it
 * (the billing records stay), after typing its name.
 */
const { t, te, locale } = useI18n();
const params = useParams({ strict: false });
const appId = computed(() => String(params.value.appId ?? ""));
const app = useApp(appId);
const navigate = useNavigate();

const role = computed(() => app.data.value?.role);
const rename = useRenameApp(appId);
const appName = ref("");
watch(() => app.data.value?.app?.name, (n) => (appName.value = n ?? ""), { immediate: true });
const toSave = computed(() => nameToSave(appName.value, app.data.value?.app?.name));

const remove = useDeleteApp(appId);
const confirming = ref(false);
const typed = ref("");
const mayDelete = computed(() => confirmsDelete(typed.value, app.data.value?.app?.name));
function askDelete() {
  typed.value = "";
  remove.reset();
  confirming.value = true;
}
async function deleteApp() {
  if (!mayDelete.value) return;
  try {
    await remove.mutateAsync();
  } catch {
    return; // the dialog shows why
  }
  confirming.value = false;
  await navigate({ to: "/app" });
}

const envs = computed(() =>
  (app.data.value?.app?.tenants ?? []).map((x) => ({
    id: x.id ?? "",
    env: envOf(x.environment),
    currency: x.currency ?? "",
    info: currencyInfo(x.currency ?? FALLBACK_CURRENCY, locale.value),
    timezone: x.timezone ?? "",
    offset: formatOffset(zoneMinutes(x.timezone ?? "UTC")),
    grace: formatDuration(x.grace_period, locale.value),
    tier: label(t, te, "tier", enumWord(x.tier, "TENANT_TIER_")),
  })),
);
</script>

<template>
  <section class="flex flex-col gap-6">
    <PageHeader flush :title="t('shell.items.appSettings')" :description="t('product.appSettings.intro')" />

    <Skeleton v-if="app.isPending.value" class="h-40 rounded-3xl" />
    <template v-else>
      <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.appSettings.general')">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.appSettings.general") }}</h2>
        <form v-if="canRenameApp(role)" class="flex flex-wrap items-end gap-3" data-testid="app-rename" @submit.prevent="rename.mutate(toSave)">
          <div class="flex min-w-48 flex-1 flex-col gap-1.5">
            <Label for="app-name">{{ t("product.createApp.name") }}</Label>
            <Input id="app-name" v-model="appName" maxlength="200" required data-testid="app-name-input" />
          </div>
          <Button type="submit" variant="outline" :disabled="rename.isPending.value || toSave === ''" data-testid="app-rename-save">{{ t("product.appSettings.rename") }}</Button>
          <ErrorText :error="rename.error.value" class="w-full" />
        </form>
        <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
          <template v-if="!canRenameApp(role)">
            <dt class="text-muted-foreground">{{ t("product.createApp.name") }}</dt>
            <dd class="font-medium">{{ app.data.value?.app?.name }}</dd>
          </template>
          <dt class="text-muted-foreground">{{ t("product.appSettings.slug") }}</dt>
          <dd class="mono">{{ app.data.value?.app?.slug }}</dd>
          <dt class="text-muted-foreground">{{ t("product.appSettings.id") }}</dt>
          <dd class="mono break-all">{{ app.data.value?.app?.id }}</dd>
        </dl>
      </section>

      <div class="grid gap-5 lg:grid-cols-2">
        <section v-for="e in envs" :key="e.id" class="surface flex flex-col gap-4 p-6" :aria-label="t(`product.env.${e.env}`)">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-lg font-semibold tracking-tight">{{ t(`product.env.${e.env}`) }}</h2>
            <StatusPill :tone="e.env === 'live' ? 'info' : 'neutral'">{{ e.tier }}</StatusPill>
          </div>
          <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
            <dt class="text-muted-foreground">{{ t("product.appSettings.currency") }}</dt>
            <dd class="font-medium" :data-testid="`currency-${e.env}`">{{ e.currency }} · {{ e.info.name }} ({{ e.info.symbol }})</dd>
            <dt class="text-muted-foreground">{{ t("product.appSettings.timezone") }}</dt>
            <dd class="font-medium" :data-testid="`timezone-${e.env}`">{{ e.timezone }} <span class="text-muted-foreground tabular-nums">{{ e.offset }}</span></dd>
            <dt class="text-muted-foreground">{{ t("product.appSettings.grace") }}</dt>
            <dd class="font-medium">{{ e.grace }}</dd>
            <dt class="text-muted-foreground">{{ t("product.appSettings.tenant") }}</dt>
            <dd class="mono text-xs break-all">{{ e.id }}</dd>
          </dl>
        </section>
      </div>

      <p class="text-muted-foreground max-w-2xl text-sm">
        {{ t("product.appSettings.moreCurrencies") }}
        <Link v-if="envs[0]" :to="`/app/t/${envs[0].id}/packages`" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("shell.items.packages") }}</Link>
      </p>

      <section v-if="canDeleteApp(role)" class="surface border-destructive/40 flex flex-col gap-3 border p-6" :aria-label="t('product.appSettings.dangerTitle')" data-testid="app-danger-zone">
        <h2 class="text-destructive text-lg font-semibold tracking-tight">{{ t("product.appSettings.dangerTitle") }}</h2>
        <div class="flex flex-wrap items-start justify-between gap-3">
          <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.appSettings.deleteBody") }}</p>
          <Button variant="destructive" data-testid="app-delete-open" @click="askDelete">{{ t("product.appSettings.delete") }}</Button>
        </div>
      </section>
    </template>

    <Dialog v-model:open="confirming">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ t("product.appSettings.confirmTitle", { name: app.data.value?.app?.name ?? "" }) }}</DialogTitle>
          <DialogDescription>{{ t("product.appSettings.confirmBody") }}</DialogDescription>
        </DialogHeader>
        <form class="flex flex-col gap-3" @submit.prevent="deleteApp">
          <div class="flex flex-col gap-1.5">
            <Label for="app-delete-name">{{ t("product.appSettings.typeName", { name: app.data.value?.app?.name ?? "" }) }}</Label>
            <Input id="app-delete-name" v-model="typed" autocomplete="off" data-testid="app-delete-confirm-input" />
          </div>
          <ErrorText :error="remove.error.value" />
          <DialogFooter>
            <Button type="button" variant="ghost" @click="confirming = false">{{ t("common.cancel") }}</Button>
            <Button type="submit" variant="destructive" :disabled="!mayDelete || remove.isPending.value" data-testid="app-delete-confirm">{{ t("product.appSettings.delete") }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </section>
</template>
