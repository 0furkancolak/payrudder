<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useAccount } from "@/features/product/api";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { ErrorState } from "@/shared/ui/error-state";
import { NativeSelect } from "@/shared/ui/native-select";
import { StatusPill } from "@/shared/ui/status-pill";
import { auditActor } from "./accounts";
import { useIntegrationAudit } from "./api";

/**
 * Who changed the live accounts, newest first: connected, replaced, removed, made default, routing changed, and by whom. The payment and e-invoice accounts
 * are in it; no setting is ever in an entry. A person is named when they belong to the organisation.
 */
const props = defineProps<{ appId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const kind = ref("");
const audit = useIntegrationAudit(() => props.appId, kind);
const account = useAccount();
const names = computed(() => new Map((account.data.value?.members ?? []).map((m) => [m.user_id ?? "", m.display_name || m.email || ""])));
const events = computed(() => audit.data.value?.pages.flatMap((p) => p.events) ?? []);
const who = (actor: string) => auditActor(actor, names.value, t("product.integrations.accounts.audit.support"));
</script>

<template>
  <section class="surface flex flex-col gap-4 p-5" data-testid="audit-card">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold tracking-tight">{{ t("product.integrations.accounts.audit.title") }}</h2>
        <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.integrations.accounts.audit.intro") }}</p>
      </div>
      <NativeSelect v-model="kind" class="w-44" :aria-label="t('product.integrations.accounts.kind')" data-testid="audit-kind">
        <option value="">{{ t("common.all") }}</option>
        <option value="payment">{{ t("product.integrations.accounts.kinds.payment") }}</option>
        <option value="einvoice">{{ t("product.integrations.accounts.kinds.einvoice") }}</option>
      </NativeSelect>
    </div>
    <ErrorState :error="audit.error.value" :retrying="audit.isFetching.value" compact @retry="audit.refetch()" />
    <p v-if="!audit.isPending.value && events.length === 0" class="text-muted-foreground text-sm" data-testid="audit-empty">{{ t("product.integrations.accounts.audit.empty") }}</p>
    <ol v-else class="divide-border flex flex-col divide-y" :aria-label="t('product.integrations.accounts.audit.title')">
      <li v-for="e in events" :key="e.id" class="flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm" :data-testid="`audit-event-${e.id}`">
        <span>
          {{ t(`product.integrations.accounts.audit.actions.${e.action}`, { actor: who(e.actor), account: e.account }) }}
          <StatusPill v-if="e.verified_remote" tone="success" class="ms-1">{{ t("product.integrations.accounts.audit.verified") }}</StatusPill>
        </span>
        <span class="text-muted-foreground flex items-center gap-2 text-xs">
          <span class="bg-muted rounded-md px-1.5 py-0.5">{{ t(`product.integrations.accounts.kinds.${e.kind}`) }}</span>
          <time :datetime="e.created_at">{{ fmt.date(e.created_at, "dateTime", "—") }}</time>
        </span>
      </li>
    </ol>
    <div v-if="audit.hasNextPage.value">
      <Button variant="outline" size="sm" :disabled="audit.isFetchingNextPage.value" data-testid="audit-more" @click="audit.fetchNextPage()">{{ t("product.integrations.accounts.audit.more") }}</Button>
    </div>
  </section>
</template>
