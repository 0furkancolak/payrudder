<script setup lang="ts">
import { Link, useSearch } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useMe } from "@/features/product/api";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Textarea } from "@/shared/ui/textarea";
import { useApply, useProgramLookup } from "./api";
import { environmentTone } from "./logic";
import TermsList from "./TermsList.vue";

/** /app/partner/apply?tenant=<id>: the seller's terms, then a form. Anybody who is signed in can open it; signed out, the sign-in brings them back here. */
const { t } = useI18n();
const search = useSearch({ strict: false });
const tenantId = computed(() => {
  const v = (search.value as { tenant?: unknown }).tenant;
  return typeof v === "string" ? v.trim() : "";
});
const me = useMe();
const lookup = useProgramLookup(tenantId);
const apply = useApply();

const name = ref("");
const method = ref<"manual" | "bank_transfer">("manual");
const details = ref("");
watch(
  () => me.data.value?.user?.display_name,
  (n) => {
    if (n && name.value === "") name.value = n;
  },
  { immediate: true },
);

function submit() {
  apply.mutate({ tenant_id: tenantId.value, name: name.value.trim(), payout_method: method.value, ...(details.value.trim() ? { payout_details: details.value.trim() } : {}) });
}
</script>

<template>
  <div class="mx-auto flex w-full max-w-2xl flex-col gap-6">
    <header>
      <h1 class="text-2xl font-semibold tracking-tight">{{ t("partner.apply.title") }}</h1>
    </header>

    <Card v-if="!tenantId" data-testid="apply-missing">
      <p class="m-0 text-sm">{{ t("partner.apply.missing") }}</p>
    </Card>
    <template v-else>
      <ErrorText :error="lookup.error.value" />
      <Skeleton v-if="lookup.isPending.value" class="h-40" />

      <template v-else-if="lookup.data.value">
        <Card>
          <div class="flex flex-wrap items-start justify-between gap-2">
            <h2 class="m-0 text-lg font-semibold break-words" dir="auto" data-testid="apply-seller">{{ lookup.data.value.seller_name }}</h2>
            <StatusPill :tone="environmentTone(lookup.data.value.environment)" data-testid="env-badge">{{ t(`partner.environment.${lookup.data.value.environment}`) }}</StatusPill>
          </div>
          <p v-if="lookup.data.value.environment === 'sandbox'" class="bg-warning-soft text-warning m-0 rounded-2xl px-4 py-3 text-sm" role="status">{{ t("partner.banner.sandbox") }}</p>
          <h3 class="m-0 text-base font-semibold">{{ t("partner.terms.title") }}</h3>
          <TermsList :program="lookup.data.value.program" />
        </Card>

        <Card v-if="apply.data.value" data-testid="apply-done" role="status">
          <h2 class="m-0 text-lg font-semibold">{{ apply.data.value.status === "active" ? t("partner.apply.activeTitle") : t("partner.apply.pendingTitle") }}</h2>
          <p class="text-muted-foreground m-0 text-sm">{{ apply.data.value.status === "active" ? t("partner.apply.activeBody") : t("partner.apply.pendingBody") }}</p>
          <div>
            <Link to="/app/partner" :search="{ open: apply.data.value.id } as never" class="text-brand-strong text-sm font-medium underline-offset-4 hover:underline">{{ t("partner.open") }}</Link>
          </div>
        </Card>
        <Card v-else-if="lookup.data.value.applied" data-testid="apply-already">
          <p class="m-0 text-sm">{{ t("errors.affiliate.portal.already_applied") }}</p>
          <div>
            <Link to="/app/partner" class="text-brand-strong text-sm font-medium underline-offset-4 hover:underline">{{ t("partner.back") }}</Link>
          </div>
        </Card>
        <Card v-else>
          <form class="grid gap-3" :aria-label="t('partner.apply.title')" @submit.prevent="submit">
            <div class="grid gap-1.5">
              <Label for="ap-name">{{ t("partner.settings.name") }}</Label>
              <Input id="ap-name" v-model="name" autocomplete="name" maxlength="200" />
            </div>
            <div class="grid gap-1.5">
              <Label for="ap-method">{{ t("affiliates.payoutMethod") }}</Label>
              <NativeSelect id="ap-method" v-model="method">
                <option value="manual">{{ t("affiliates.methods.manual") }}</option>
                <option value="bank_transfer">{{ t("affiliates.methods.bank_transfer") }}</option>
              </NativeSelect>
            </div>
            <div class="grid gap-1.5">
              <Label for="ap-details">{{ t("affiliates.payoutDetails") }}</Label>
              <Textarea id="ap-details" v-model="details" rows="3" maxlength="1000" />
              <p class="text-muted-foreground m-0 text-xs">{{ t("partner.settings.detailsHint") }}</p>
            </div>
            <ErrorText :error="apply.error.value" />
            <div><Button type="submit" :disabled="apply.isPending.value">{{ t("partner.apply.submit") }}</Button></div>
          </form>
        </Card>
      </template>
    </template>
  </div>
</template>
