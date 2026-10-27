<script setup lang="ts">
import { ArrowRight } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { tenantOf } from "@/shared/lib/environment";
import { Button } from "@/shared/ui/button";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { StatusPill } from "@/shared/ui/status-pill";
import { useApp, usePromote } from "./api";
import { promoteSummary } from "./promote";

/**
 * Copies the sandbox's configuration (meters, plans, taxes, coupons, mail, webhook endpoints) to live. Always a preview first: the diff is shown, and
 * "Apply" sends back the hash of exactly that diff, so the server refuses (and the page says so) when the configuration moved in between. It only adds;
 * a live object that differs is a conflict and is left alone. A webhook endpoint made by the copy shows its new signing secret once.
 */
const props = defineProps<{ appId: string }>();
const { t } = useI18n();
const app = useApp(() => props.appId);
const promote = usePromote(() => props.appId);
const canPromote = computed(() => app.data.value?.role === "owner" || app.data.value?.role === "admin");
const hasLive = computed(() => tenantOf(app.data.value?.app ?? undefined, "live") !== "");
const result = computed(() => promote.data.value);
const summary = computed(() => promoteSummary(result.value));
const secrets = computed(() => (result.value?.changes ?? []).filter((c) => c.secret));

function preview() {
  promote.mutate({ dryRun: true });
}
function apply() {
  promote.mutate({ dryRun: false, planHash: result.value?.plan_hash });
}
</script>

<template>
  <section v-if="hasLive" class="surface flex flex-col gap-4 p-6" :aria-label="t('product.promote.title')" data-testid="promote">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.promote.title") }}</h2>
        <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.promote.intro") }}</p>
      </div>
      <Button variant="outline" :disabled="!canPromote || promote.isPending.value" data-testid="promote-preview" @click="preview"><ArrowRight class="rtl:rotate-180" />{{ t("product.promote.preview") }}</Button>
    </div>
    <p v-if="!canPromote" class="text-muted-foreground text-xs">{{ t("product.promote.adminOnly") }}</p>
    <ErrorText :error="promote.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />

    <template v-if="result">
      <div class="flex flex-wrap items-center gap-2" data-testid="promote-summary">
        <StatusPill :tone="result.dry_run ? 'neutral' : 'success'">{{ result.dry_run ? t("product.promote.previewed") : t("product.promote.applied") }}</StatusPill>
        <StatusPill tone="success">{{ t("product.promote.created", { n: summary.created }) }}</StatusPill>
        <StatusPill tone="neutral">{{ t("product.promote.unchanged", { n: summary.unchanged }) }}</StatusPill>
        <StatusPill :tone="summary.conflicts > 0 ? 'warning' : 'neutral'">{{ t("product.promote.conflicts", { n: summary.conflicts }) }}</StatusPill>
        <StatusPill v-if="summary.skipped > 0" tone="neutral">{{ t("product.promote.skipped", { n: summary.skipped }) }}</StatusPill>
        <StatusPill v-if="summary.failed > 0" tone="danger">{{ t("product.promote.failed", { n: summary.failed }) }}</StatusPill>
      </div>

      <p v-if="summary.nothing" class="text-muted-foreground text-sm" data-testid="promote-nothing">{{ t("product.promote.nothing") }}</p>
      <ul v-else class="divide-border/70 divide-y text-sm" data-testid="promote-changes">
        <li v-for="(c, i) in summary.rows" :key="i" class="flex flex-wrap items-center gap-x-3 gap-y-1 py-2" :data-action="c.action">
          <span class="text-muted-foreground w-24 shrink-0 text-xs">{{ t(`product.promote.family.${c.family}`) }}</span>
          <span class="mono min-w-0 flex-1 basis-40 break-all">{{ c.key }}</span>
          <StatusPill :tone="c.action === 'create' ? 'success' : c.action === 'conflict' ? 'warning' : 'neutral'">{{ t(`product.promote.action.${c.action}`) }}</StatusPill>
          <span v-if="c.detail" class="text-muted-foreground basis-full text-xs">{{ c.detail }}</span>
          <span v-if="c.error" class="text-destructive basis-full text-xs" role="alert">{{ c.error }}</span>
        </li>
      </ul>
      <p v-if="summary.conflicts > 0" class="text-muted-foreground text-xs">{{ t("product.promote.conflictNote") }}</p>

      <div v-if="result.dry_run && summary.created > 0" class="flex flex-wrap items-center gap-3 border-t pt-4">
        <Button :disabled="!canPromote || promote.isPending.value" data-testid="promote-apply" @click="apply">{{ t("product.promote.apply", { n: summary.created }) }}</Button>
        <p class="text-muted-foreground text-xs">{{ t("product.promote.applyNote") }}</p>
      </div>

      <div v-if="secrets.length > 0" class="bg-warning-soft flex flex-col gap-2 rounded-2xl p-4" data-testid="promote-secrets">
        <p class="text-warning text-sm font-medium">{{ t("product.promote.secretOnce") }}</p>
        <div v-for="c in secrets" :key="c.key" class="flex flex-col gap-1">
          <p class="mono text-xs break-all">{{ c.key }}</p>
          <CopyField :value="c.secret ?? ''" :label="t('product.promote.secretLabel')" :copy-label="t('product.keys.copy')" />
        </div>
      </div>
    </template>
  </section>
</template>
