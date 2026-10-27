<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { ArrowLeftRight, FlaskConical, Radio } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import type { App } from "@/features/product/api";
import type { Environment } from "@/shared/lib/environment";
import { ALL_APPS } from "@/shared/lib/scope";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { envTarget } from "./scopeNav";

/**
 * The sandbox / live switch. It is never a one-click toggle: the pill says which environment the pages show (and stays on every page), and choosing
 * the other one opens a dialog that says what changes (only that environment's data is shown; the app itself is not affected; in the sandbox nothing
 * reaches a real provider or customer) before anything moves.
 */
const props = defineProps<{ env: Environment; scope: string; app?: App; path: string }>();
const { t } = useI18n();
const navigate = useNavigate();

const asking = ref(false);
const other = computed<Environment>(() => (props.env === "live" ? "sandbox" : "live"));
const target = computed(() => envTarget(props.path, props.scope, props.app, other.value));
/** An app that has no live environment (a sandbox-only deployment) cannot be switched to it. */
const available = computed(() => target.value !== undefined);
const what = computed(() => props.app?.name ?? "");

function confirm() {
  const to = target.value;
  asking.value = false;
  if (to) void navigate({ to: to.to, search: to.search as never });
}
</script>

<template>
  <button
    type="button"
    data-testid="env-switch"
    :data-env="env"
    :class="[
      'inline-flex h-10 shrink-0 items-center gap-2 rounded-xl border px-3 text-sm font-medium transition-colors',
      env === 'sandbox' ? 'bg-warning-soft text-warning border-warning/30' : 'bg-card text-foreground border-border/60 shadow-card hover:bg-accent',
    ]"
    :aria-label="t('product.envSwitch.open', { env: t(`product.env.${env}`) })"
    aria-haspopup="dialog"
    :aria-expanded="asking"
    :disabled="!available"
    @click="asking = true"
  >
    <FlaskConical v-if="env === 'sandbox'" class="size-4" aria-hidden="true" />
    <Radio v-else class="size-4" aria-hidden="true" />
    <span data-testid="env-switch-current">{{ t(`product.env.${env}`) }}</span>
    <ArrowLeftRight class="text-muted-foreground size-3.5" aria-hidden="true" />
  </button>

  <Dialog v-model:open="asking">
    <DialogContent class="sm:max-w-md" data-testid="env-switch-dialog">
      <DialogHeader>
        <DialogTitle>{{ t(`product.envSwitch.title.${other}`) }}</DialogTitle>
        <DialogDescription>{{ t(`product.envSwitch.intro.${other}`) }}</DialogDescription>
      </DialogHeader>
      <ul class="text-muted-foreground grid gap-2.5 text-sm" data-testid="env-switch-points">
        <li>{{ scope === ALL_APPS ? t("product.envSwitch.unaffectedAll") : t("product.envSwitch.unaffected", { app: what }) }}</li>
        <li>{{ t(`product.envSwitch.onlyData.${other}`) }}</li>
        <li class="text-foreground font-medium">{{ t(`product.envSwitch.providers.${other}`) }}</li>
      </ul>
      <DialogFooter>
        <Button variant="ghost" data-testid="env-switch-cancel" @click="asking = false">{{ t("common.cancel") }}</Button>
        <Button data-testid="env-switch-confirm" @click="confirm">{{ t(`product.envSwitch.confirm.${other}`) }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
