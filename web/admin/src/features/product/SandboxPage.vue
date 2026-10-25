<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { Check, Circle, Loader, RotateCcw, X } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { tenantOf } from "@/shared/lib/environment";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { StatusPill } from "@/shared/ui/status-pill";
import { useApp, useResetSandbox, useSandboxReset } from "./api";
import { SCENARIOS } from "./golive";

/**
 * The sandbox's own tools: wipe its data and start again (the configuration stays), and the catalogue of payment scenarios a sandbox customer can take.
 * The reset is a job in the background; the page follows it step by step. Only the sandbox can be reset: there is no such button for live.
 */
const { t } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const appId = computed(() => String(params.value.appId ?? ""));
const app = useApp(appId);
const status = useSandboxReset(appId);
const reset = useResetSandbox(appId);
const confirming = ref(false);

const job = computed(() => status.data.value);
const running = computed(() => job.value?.state === "queued" || job.value?.state === "running");
/** The step being worked on: the first one not finished (steps run in order). */
const next = computed(() => job.value?.steps?.find((s) => s.state === "pending")?.name);
const canReset = computed(() => app.data.value?.role === "owner" || app.data.value?.role === "admin");
const hasSandbox = computed(() => tenantOf(app.data.value?.app, "sandbox") !== "");
const rows = (deleted: Record<string, number> | undefined) => Object.entries(deleted ?? {}).sort(([a], [b]) => a.localeCompare(b));
const total = (deleted: Record<string, number> | undefined) => Object.values(deleted ?? {}).reduce((n, v) => n + v, 0);

function go() {
  reset.mutate(undefined, { onSuccess: () => (confirming.value = false), onError: () => (confirming.value = false) });
}
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="px-1">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("shell.items.sandbox") }}</h1>
        <StatusPill tone="neutral">{{ t("product.env.sandbox") }}</StatusPill>
      </div>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ t("product.sandbox.intro") }}</p>
    </header>

    <section v-if="hasSandbox" class="surface flex flex-col gap-4 p-6" :aria-label="t('product.sandbox.resetTitle')" data-testid="sandbox-reset">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 class="text-lg font-semibold tracking-tight">{{ t("product.sandbox.resetTitle") }}</h2>
          <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.sandbox.resetBody") }}</p>
        </div>
        <Button variant="outline" :disabled="!canReset || running || reset.isPending.value" data-testid="sandbox-reset-open" @click="confirming = true"><RotateCcw />{{ t("product.sandbox.reset") }}</Button>
      </div>
      <p v-if="!canReset" class="text-muted-foreground text-xs">{{ t("product.promote.adminOnly") }}</p>
      <ErrorText :error="reset.error.value ?? status.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />

      <div v-if="job" class="flex flex-col gap-3 border-t pt-4" data-testid="sandbox-reset-job" :data-state="job.state">
        <div class="flex flex-wrap items-center gap-2">
          <StatusPill :tone="job.state === 'done' ? 'success' : job.state === 'failed' ? 'danger' : 'warning'">{{ t(`product.sandbox.state.${job.state}`) }}</StatusPill>
          <span class="text-muted-foreground text-xs">{{ t("product.sandbox.started", { date: fmt.date(job.create_time, "day") }) }}</span>
          <span v-if="job.kind === 'retention'" class="text-muted-foreground text-xs">{{ t("product.sandbox.byRetention") }}</span>
        </div>
        <ol class="flex flex-col gap-1.5 text-sm">
          <li v-for="s in job.steps" :key="s.name" class="flex flex-wrap items-center gap-x-3 gap-y-1" :data-step="s.name" :data-state="s.state">
            <span class="flex size-5 shrink-0 items-center justify-center" aria-hidden="true">
              <Check v-if="s.state === 'done'" class="text-success size-4" /><X v-else-if="s.state === 'failed'" class="text-destructive size-4" />
              <Loader v-else-if="running && s.state === 'pending' && s.name === next" class="size-4 animate-spin" /><Circle v-else class="text-muted-foreground size-3.5" />
            </span>
            <span class="w-28 shrink-0 font-medium">{{ t(`product.sandbox.step.${s.name}`) }}</span>
            <span v-if="s.state === 'done'" class="text-muted-foreground text-xs tabular">{{ t("product.sandbox.deleted", { n: total(s.deleted) }) }}</span>
            <span v-else-if="s.state === 'skipped'" class="text-muted-foreground text-xs">{{ t("product.sandbox.skipped") }}</span>
            <span v-if="s.error" class="text-destructive basis-full ps-8 text-xs" role="alert">{{ s.error }}</span>
            <span v-if="s.state === 'done' && rows(s.deleted).length > 0" class="text-muted-foreground basis-full ps-8 text-xs">{{ rows(s.deleted).map(([k, v]) => `${k} ${v}`).join(" · ") }}</span>
          </li>
        </ol>
        <p v-if="job.state === 'failed'" class="text-muted-foreground text-xs">{{ t("product.sandbox.failedNote") }}</p>
      </div>
      <p v-else-if="!status.isPending.value" class="text-muted-foreground text-xs">{{ t("product.sandbox.neverReset") }}</p>
    </section>

    <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.sandbox.scenariosTitle')" data-testid="scenarios">
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.sandbox.scenariosTitle") }}</h2>
        <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.sandbox.scenariosBody") }}</p>
      </div>
      <ul class="divide-border/70 divide-y">
        <li v-for="s in SCENARIOS" :key="s.scenario" class="flex flex-wrap items-start gap-x-4 gap-y-1 py-3" :data-scenario="s.scenario">
          <code class="mono min-w-0 basis-64 text-sm break-all">pm_sandbox_{{ s.scenario }}</code>
          <p class="text-muted-foreground min-w-0 flex-1 basis-60 text-sm">{{ t(`product.sandbox.scenario.${s.scenario}`) }}</p>
          <StatusPill tone="neutral">{{ t(`product.sandbox.outcome.${s.outcome}`) }}</StatusPill>
          <StatusPill v-if="'default' in s" tone="info">{{ t("product.sandbox.default") }}</StatusPill>
        </li>
      </ul>
      <p class="text-muted-foreground text-xs">{{ t("product.sandbox.simulateNote") }}</p>
    </section>

    <Dialog v-model:open="confirming">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ t("product.sandbox.confirmTitle") }}</DialogTitle>
          <DialogDescription>{{ t("product.sandbox.confirmBody") }}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="ghost" @click="confirming = false">{{ t("common.cancel") }}</Button>
          <Button variant="destructive" :disabled="reset.isPending.value" data-testid="sandbox-reset-confirm" @click="go">{{ t("product.sandbox.reset") }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </section>
</template>
