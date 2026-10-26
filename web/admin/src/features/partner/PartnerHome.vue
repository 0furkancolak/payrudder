<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useResendVerification } from "@/features/product/api";
import { toApiError } from "@/shared/api/client";
import { personToken } from "@/shared/lib/session";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useAffiliations, useClaim } from "./api";
import { affiliateTone, environmentTone, sortAffiliations } from "./logic";
import PartnerDetail from "./PartnerDetail.vue";
import StatusBanner from "./StatusBanner.vue";
import TermsList from "./TermsList.vue";

/** The partner portal's start: every seller the person is a partner of. */
const { t } = useI18n();
const list = useAffiliations();
const claim = useClaim();
const resend = useResendVerification();
const affiliations = computed(() => sortAffiliations(list.data.value));

/** The affiliation whose sheet is open, as its place among the cards; a link from the apply page names one with `?open=<id>`. */
const shown = ref<number | null>(null);
const current = computed(() => (shown.value === null ? undefined : affiliations.value[shown.value]));
const open = (id: string) => {
  const at = affiliations.value.findIndex((a) => a.id === id);
  if (at >= 0) shown.value = at;
};
useOpenParam("open", () => list.isSuccess.value, open);

// The records a seller made for the person's verified address are claimed once per session (idempotent on the server), then the list is read again.
let claimedFor: string | null = null;
onMounted(() => {
  if (claimedFor === personToken.value) return;
  claimedFor = personToken.value;
  claim.mutate();
});
const unverified = computed(() => claim.error.value !== null && toApiError(claim.error.value).key === "identity.email.not_verified");
const claimProblem = computed(() => (claim.error.value && !unverified.value ? claim.error.value : null));
</script>

<template>
  <div class="flex flex-col gap-6">
    <header>
      <h1 class="text-2xl font-semibold tracking-tight">{{ t("partner.title") }}</h1>
      <p class="text-muted-foreground text-sm">{{ t("partner.intro") }}</p>
    </header>

    <div v-if="unverified" role="status" data-testid="verify-hint" class="bg-warning-soft text-warning flex flex-wrap items-center justify-between gap-3 rounded-2xl px-4 py-3 text-sm">
      <span>{{ t("partner.verifyHint") }}</span>
      <Button type="button" size="sm" variant="outline" :disabled="resend.isPending.value || resend.isSuccess.value" @click="resend.mutate()">
        {{ resend.isSuccess.value ? t("partner.verifySent") : t("partner.verifyResend") }}
      </Button>
    </div>
    <ErrorText :error="claimProblem" />
    <ErrorText :error="list.error.value" />

    <Skeleton v-if="list.isPending.value" class="h-40" />
    <Card v-else-if="list.isSuccess.value && !affiliations.length" data-testid="partner-empty">
      <h2 class="text-lg font-semibold">{{ t("partner.empty.title") }}</h2>
      <p class="text-muted-foreground m-0 text-sm">{{ t("partner.empty.body") }}</p>
    </Card>

    <div v-else class="grid gap-4 lg:grid-cols-2">
      <Card v-for="a in affiliations" :key="a.id" :data-affiliation="a.id" :aria-label="a.seller_name">
        <div class="flex flex-wrap items-start justify-between gap-2">
          <div class="min-w-0">
            <h2 class="m-0 text-lg font-semibold break-words" dir="auto">{{ a.seller_name }}</h2>
            <p class="text-muted-foreground m-0 text-xs">{{ t("partner.code") }}: <span class="mono">{{ a.code }}</span></p>
          </div>
          <div class="flex flex-wrap gap-1.5">
            <StatusPill :tone="environmentTone(a.environment)" data-testid="env-badge">{{ t(`partner.environment.${a.environment}`) }}</StatusPill>
            <StatusPill :tone="affiliateTone(a.status)" data-testid="status-badge">{{ t(`partner.status.${a.status}`) }}</StatusPill>
          </div>
        </div>
        <StatusBanner :affiliation="a" />
        <CopyField v-if="a.link" :value="a.link" :label="t('partner.link')" :copy-label="t('partner.copyLink')" testid="affiliation-link" />
        <TermsList :program="a.program" />
        <div>
          <Button type="button" variant="link" class="h-auto p-0" :data-testid="`open-${a.id}`" @click="open(a.id ?? '')">{{ t("partner.open") }}</Button>
        </div>
      </Card>
    </div>
  </div>

  <DetailSheet v-model:index="shown" :total="affiliations.length" :title="current?.seller_name ?? ''" :description="current ? `${t('partner.code')}: ${current.code}` : undefined" wide>
    <PartnerDetail v-if="current" :id="current.id ?? ''" :key="current.id" />
  </DetailSheet>
</template>
