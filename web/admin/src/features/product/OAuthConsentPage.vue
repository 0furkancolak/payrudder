<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { Checkbox } from "@/shared/ui/checkbox";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { PageHeader } from "@/shared/ui/page-header";
import { Skeleton } from "@/shared/ui/skeleton";
import { useApps } from "./api";
import type { KeyPermission } from "./apiKeys";
import { groupScopes, oauthParams, useDecideConsent, useOAuthRequest } from "./oauth";

/**
 * The consent page of the OAuth authorization server (docs/plans/20-mcp.md, "OAuth design"; default, not agreed). An assistant sent the person here with an authorization
 * request in the address; the gateway has checked it and read the client's metadata document. The person sees who asks (the name the client chose, next to the address it
 * is published at and the address the browser returns to: the name alone proves nothing), chooses the app and environment it may act on, and keeps or takes away scopes.
 * A request the gateway cannot use is shown as an error and nobody is redirected anywhere. The decision's answer is the address to open.
 */
const { t } = useI18n();
const params = oauthParams(window.location.search);
const request = useOAuthRequest(params);
const apps = useApps();
const decide = useDecideConsent();

const info = computed(() => request.data.value);
const groups = computed(() => groupScopes(info.value?.scopes ?? []));
const kept = ref(new Set<KeyPermission>());
watch(info, (i) => (kept.value = new Set((i?.scopes ?? []) as KeyPermission[])), { immediate: true });
function toggle(p: KeyPermission, on: boolean): void {
  const next = new Set(kept.value);
  if (on) next.add(p);
  else next.delete(p);
  kept.value = next;
}

interface Option {
  id: string;
  app: string;
  env: "sandbox" | "live";
  disabled: boolean;
}
const options = computed<Option[]>(() =>
  (apps.data.value?.apps ?? []).flatMap((a) =>
    (a.tenants ?? [])
      .filter((tn) => tn.id)
      .map((tn): Option => {
        const env = tn.environment === "ENVIRONMENT_LIVE" ? "live" : "sandbox";
        return { id: tn.id as string, app: a.name, env, disabled: env === "live" && info.value?.allow_live !== true };
      }),
  ),
);
const anyLiveOff = computed(() => options.value.some((o) => o.disabled));
const chosen = ref("");
// The first environment that may be chosen, a sandbox by preference, is ticked: the safest answer is the one a quick "Allow" gives.
watch(
  options,
  (os) => {
    if (!os.some((o) => o.id === chosen.value)) chosen.value = (os.find((o) => !o.disabled && o.env === "sandbox") ?? os.find((o) => !o.disabled))?.id ?? "";
  },
  { immediate: true },
);

const canAllow = computed(() => chosen.value !== "" && kept.value.size > 0 && !decide.isPending.value && !decide.isSuccess.value);
function send(decision: "allow" | "deny"): void {
  decide.mutate(
    { params, decision, ...(decision === "allow" ? { tenantId: chosen.value, scopes: [...kept.value] } : {}) },
    { onSuccess: (r) => window.location.assign(r.redirect_to) },
  );
}
const envLabel = (o: Option) => t("product.oauth.environment", { app: o.app, env: t(o.env === "live" ? "product.oauth.live" : "product.oauth.sandbox") });
</script>

<template>
  <section class="flex flex-col gap-6" data-testid="oauth-consent">
    <PageHeader flush :title="t('product.oauth.title')" />
    <Skeleton v-if="request.isPending.value" class="h-48 max-w-2xl rounded-2xl" />
    <div v-else-if="request.isError.value" class="flex max-w-2xl flex-col gap-3" data-testid="oauth-invalid">
      <ErrorState :error="request.error.value" :title="t('product.oauth.invalid')" />
      <p class="text-muted-foreground text-sm">{{ t("product.oauth.invalidBody") }}</p>
    </div>

    <div v-else-if="info" class="surface flex max-w-2xl flex-col gap-6 p-6">
      <div class="flex flex-col gap-2">
        <p class="text-sm leading-relaxed" data-testid="oauth-intro">{{ t("product.oauth.intro", { client: info.client_name }) }}</p>
        <p class="text-muted-foreground text-xs" data-testid="oauth-client-host">{{ t("product.oauth.clientAddress", { host: info.client_host }) }}</p>
        <p class="text-muted-foreground text-xs" data-testid="oauth-redirect-host">{{ t("product.oauth.redirect", { host: info.redirect_host }) }}</p>
        <p v-if="info.redirect_loopback" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status" data-testid="oauth-loopback">{{ t("product.oauth.loopback") }}</p>
      </div>

      <fieldset class="flex flex-col gap-2">
        <legend class="mb-1 text-base font-semibold tracking-tight">{{ t("product.oauth.where") }}</legend>
        <p v-if="apps.isSuccess.value && options.length === 0" class="text-muted-foreground text-sm" data-testid="oauth-no-apps">{{ t("product.oauth.noApps") }}</p>
        <label
          v-for="o in options"
          :key="o.id"
          :class="['border-border/60 flex items-center gap-3 rounded-2xl border px-4 py-3 text-sm', o.disabled ? 'text-muted-foreground opacity-60' : 'cursor-pointer']"
        >
          <input v-model="chosen" type="radio" name="tenant" class="accent-primary size-4" :value="o.id" :disabled="o.disabled" :data-testid="`oauth-env-${o.env}-${o.app}`" />
          <span>{{ envLabel(o) }}</span>
        </label>
        <p v-if="anyLiveOff" class="text-muted-foreground text-xs" data-testid="oauth-live-off">{{ t("product.oauth.liveOff") }}</p>
      </fieldset>

      <fieldset class="flex flex-col gap-2">
        <legend class="mb-1 text-base font-semibold tracking-tight">{{ t("product.oauth.what") }}</legend>
        <ul class="flex flex-col gap-1.5 text-sm">
          <li v-for="g in groups" :key="g.resource" class="flex flex-wrap items-center gap-x-4 gap-y-1">
            <span class="min-w-48 font-medium">{{ t(`product.keys.resources.${g.resource}`) }}</span>
            <label v-if="g.read" class="flex items-center gap-2">
              <Checkbox :model-value="kept.has(g.read)" :data-testid="`oauth-scope-${g.read}`" :aria-label="`${t(`product.keys.resources.${g.resource}`)}: ${t('product.oauth.reads')}`" @update:model-value="toggle(g.read, $event === true)" />
              <span>{{ t("product.oauth.reads") }}</span>
            </label>
            <label v-if="g.write" class="flex items-center gap-2">
              <Checkbox :model-value="kept.has(g.write)" :data-testid="`oauth-scope-${g.write}`" :aria-label="`${t(`product.keys.resources.${g.resource}`)}: ${t('product.oauth.writes')}`" @update:model-value="toggle(g.write, $event === true)" />
              <span class="text-warning font-medium">{{ t("product.oauth.writes") }}</span>
            </label>
          </li>
        </ul>
        <p class="text-muted-foreground text-xs">{{ t("product.oauth.scopesNote") }}</p>
        <p v-if="kept.size === 0" class="text-destructive text-xs" role="alert" data-testid="oauth-no-scopes">{{ t("product.oauth.noScopes") }}</p>
      </fieldset>

      <ErrorText :error="decide.error.value" />
      <div class="flex flex-wrap items-center gap-2">
        <Button type="button" :disabled="!canAllow" data-testid="oauth-allow" @click="send('allow')">{{ t("product.oauth.allow") }}</Button>
        <Button type="button" variant="outline" :disabled="decide.isPending.value || decide.isSuccess.value" data-testid="oauth-deny" @click="send('deny')">{{ t("product.oauth.deny") }}</Button>
        <span v-if="decide.isSuccess.value" class="text-muted-foreground text-sm" role="status" data-testid="oauth-going">{{ t("product.oauth.going") }}</span>
      </div>
    </div>
  </section>
</template>
