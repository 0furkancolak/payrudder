<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorState } from "@/shared/ui/error-state";
import { PageHeader } from "@/shared/ui/page-header";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useMe } from "./api";
import { useOAuthGrants, useRevokeOAuthGrant } from "./oauth";
import CodePrompt from "./CodePrompt.vue";
import RecoveryCodes from "./RecoveryCodes.vue";
import TwoFactorSetup from "./TwoFactorSetup.vue";
import { RECOVERY_CODES_LOW } from "./twoFactor";
import { useDisableTwoFactor, useRegenerateRecoveryCodes, useTwoFactorStatus } from "./twoFactorApi";

/**
 * The person's own security: two-factor sign-in with an authenticator app (turn it on, turn it off, new recovery codes). It is the one page a person can use
 * while an organisation they own requires two-factor sign-in and they have none yet: the banner says why, and the rest of the console waits until it is on.
 */
const { t } = useI18n();
const fmt = useFormat();
const me = useMe();
const status = useTwoFactorStatus();
const disable = useDisableTwoFactor();
const regenerate = useRegenerateRecoveryCodes();
const grants = useOAuthGrants();
const revokeGrant = useRevokeOAuthGrant();

const email = computed(() => me.data.value?.user?.email ?? "");
const enabled = computed(() => status.data.value?.enabled === true);
const required = computed(() => status.data.value?.required_by_organisation === true);
const mustSetUp = computed(() => status.data.value?.setup_required ?? me.data.value?.user?.two_factor_setup_required ?? false);
const left = computed(() => status.data.value?.recovery_codes_remaining ?? 0);
const low = computed(() => enabled.value && left.value <= RECOVERY_CODES_LOW);

const setting = ref(false);
const turningOff = ref(false);
const renewing = ref(false);
watch(turningOff, (o) => o && disable.reset());
watch(renewing, (o) => o && regenerate.reset());
// The new recovery codes are in the mutation's data until "Done" resets it (it is not cached: gcTime 0).
const newCodes = computed(() => regenerate.data.value?.recovery_codes ?? null);

function turnOff(code: string): void {
  disable.mutate(code, { onSuccess: () => (turningOff.value = false) });
}
function renew(code: string): void {
  regenerate.mutate(code, { onSuccess: () => (renewing.value = false) });
}
</script>

<template>
  <section class="flex flex-col gap-6">
    <PageHeader flush :title="t('product.security.title')" :description="t('product.security.intro')" />
    <p v-if="mustSetUp" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status" data-testid="setup-required">{{ t("product.security.setupRequired") }}</p>
    <ErrorState :error="status.error.value" :retrying="status.isFetching.value" @retry="status.refetch()" />

    <section class="surface flex max-w-2xl flex-col gap-4 p-6" :aria-label="t('product.twoFactor.title')" data-testid="two-factor-card">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.twoFactor.title") }}</h2>
        <StatusPill v-if="status.data.value" :tone="enabled ? 'success' : 'neutral'" data-testid="two-factor-status">{{ enabled ? t("product.twoFactor.on") : t("product.twoFactor.off") }}</StatusPill>
      </div>
      <Skeleton v-if="status.isPending.value" class="h-24 rounded-2xl" />
      <template v-else-if="status.data.value">
        <p class="text-muted-foreground text-sm">{{ enabled ? t("product.twoFactor.onBody") : t("product.twoFactor.offBody") }}</p>
        <dl v-if="enabled" class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
          <dt class="text-muted-foreground">{{ t("product.twoFactor.since") }}</dt>
          <dd data-testid="two-factor-since">{{ fmt.date(status.data.value.enable_time, "dateTime", "—") }}</dd>
          <dt class="text-muted-foreground">{{ t("product.twoFactor.codesLeft") }}</dt>
          <dd class="tabular-nums" data-testid="two-factor-left">{{ left }}</dd>
        </dl>
        <p v-if="low" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status" data-testid="two-factor-low">{{ t("product.twoFactor.codesLow", { n: left }, left) }}</p>
        <div class="flex flex-wrap gap-2">
          <Button v-if="!enabled" type="button" data-testid="two-factor-enable" @click="setting = true">{{ t("product.twoFactor.turnOn") }}</Button>
          <template v-else>
            <Button type="button" variant="outline" data-testid="two-factor-regenerate" @click="renewing = true">{{ t("product.twoFactor.regenerate") }}</Button>
            <Button type="button" variant="outline" :disabled="required" aria-describedby="two-factor-required" data-testid="two-factor-disable" @click="turningOff = true">{{ t("product.twoFactor.turnOff") }}</Button>
          </template>
        </div>
        <p v-if="enabled && required" id="two-factor-required" class="text-muted-foreground text-xs" data-testid="two-factor-required">{{ t("product.twoFactor.requiredNote") }}</p>
      </template>
    </section>

    <!-- The assistants the person has let in (OAuth, docs/plans/20-mcp.md). Absent when the deployment does not offer it (the list answers 404). -->
    <section v-if="grants.isSuccess.value" class="surface flex max-w-2xl flex-col gap-4 p-6" :aria-label="t('product.security.oauth.title')" data-testid="oauth-grants">
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.security.oauth.title") }}</h2>
        <p class="text-muted-foreground mt-1 text-sm">{{ t("product.security.oauth.intro") }}</p>
      </div>
      <p v-if="(grants.data.value?.grants ?? []).length === 0" class="text-muted-foreground text-sm" data-testid="oauth-grants-empty">{{ t("product.security.oauth.empty") }}</p>
      <ul v-else class="divide-border/60 flex flex-col divide-y">
        <li v-for="g in grants.data.value?.grants ?? []" :key="g.id" class="flex flex-wrap items-center justify-between gap-3 py-3" :data-testid="`oauth-grant-${g.id}`">
          <div class="min-w-0">
            <p class="truncate font-medium">{{ g.client_name }}</p>
            <p class="text-muted-foreground text-xs">
              {{ t("product.security.oauth.detail", { app: g.app_name ?? "", env: t(g.environment === "ENVIRONMENT_LIVE" ? "product.oauth.live" : "product.oauth.sandbox"), n: g.scopes.length }) }}
              · {{ g.last_used_time ? t("product.security.oauth.lastUsed", { date: fmt.date(g.last_used_time, "dateTime", "—") }) : t("product.security.oauth.neverUsed") }}
            </p>
          </div>
          <Button type="button" variant="outline" size="sm" :disabled="revokeGrant.isPending.value" :data-testid="`oauth-revoke-${g.id}`" @click="revokeGrant.mutate(g.id)">{{ t("product.security.oauth.revoke") }}</Button>
        </li>
      </ul>
    </section>
  </section>

  <TwoFactorSetup v-model:open="setting" :email="email" />

  <CodePrompt
    v-model:open="turningOff"
    :title="t('product.twoFactor.disableTitle')"
    :body="t('product.twoFactor.disableBody')"
    :submit="t('product.twoFactor.turnOff')"
    destructive
    :pending="disable.isPending.value"
    :error="disable.error.value"
    @submit="turnOff"
  />
  <CodePrompt
    v-model:open="renewing"
    :title="t('product.twoFactor.regenerateTitle')"
    :body="t('product.twoFactor.regenerateBody')"
    :submit="t('product.twoFactor.regenerate')"
    :pending="regenerate.isPending.value"
    :error="regenerate.error.value"
    @submit="renew"
  />

  <!-- The new codes: the dialog stays until "Done", after the person has said they saved them. -->
  <Dialog :open="newCodes !== null">
    <DialogContent class="sm:max-w-lg" :show-close-button="false" data-testid="new-recovery-codes" @interact-outside.prevent @escape-key-down.prevent>
      <DialogHeader>
        <DialogTitle>{{ t("product.twoFactor.setup.codes.title") }}</DialogTitle>
        <DialogDescription>{{ t("product.twoFactor.regeneratedBody") }}</DialogDescription>
      </DialogHeader>
      <RecoveryCodes v-if="newCodes" :codes="newCodes" :email="email" @done="regenerate.reset()" />
    </DialogContent>
  </Dialog>
</template>
