<script setup lang="ts">
import { askConfirm } from "@/shared/lib/confirm";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { StatusPill } from "@/shared/ui/status-pill";
import { useEInvoiceActions, type EInvoiceMode, type EInvoiceProvider } from "./api";
import { BRANDS } from "./brands";
import { eInvoiceState } from "./status";

/**
 * The side sheet that connects the e-invoice integrator of the live environment (or replaces its credentials, tests it, or disconnects it). An environment has
 * one integrator, so connecting this one replaces the other; the sheet says so. The credentials are typed once, are password fields and are never shown again.
 * The sandbox needs no connection: its documents go to the built-in simulator.
 */
const props = defineProps<{
  provider: EInvoiceProvider | "";
  appId: string;
  /** What is stored now (no credential is in it). */
  account: { provider?: string; mode?: string } | null | undefined;
  isOwner: boolean;
  /** The seller's tax id from the billing profile, for the test (an integrator that can look up a registered business is asked about it). */
  taxId: string;
}>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const actions = useEInvoiceActions(() => props.appId);

const mode = ref<EInvoiceMode>("production");
const apiKey = ref("");
const baseUrl = ref("");
const hookUrl = ref("");
const hookSecret = ref("");
const problem = ref("");

const here = computed(() => (props.provider ? eInvoiceState(props.account, props.provider) : { state: "none" as const, mode: "" }));
const other = computed(() => !!props.account?.provider && props.provider !== "" && props.account.provider !== props.provider);
const name = computed(() => (props.provider ? (BRANDS[props.provider].name ?? t(`product.integrations.names.${props.provider}`)) : ""));

watch(
  () => [open.value, props.provider] as const,
  ([isOpen]) => {
    if (!isOpen) return;
    mode.value = "production";
    apiKey.value = baseUrl.value = hookUrl.value = hookSecret.value = problem.value = "";
    actions.save.reset();
    actions.remove.reset();
    actions.test.reset();
  },
  { immediate: true },
);

function credentials(): Record<string, string> {
  if (props.provider === "nilvera") return { api_key: apiKey.value.trim(), ...(baseUrl.value.trim() ? { base_url: baseUrl.value.trim() } : {}) };
  return { url: hookUrl.value.trim(), secret: hookSecret.value };
}

function submit() {
  if (props.provider === "" || !props.isOwner) return;
  actions.save.mutate(
    { provider: props.provider, mode: mode.value, credentials: credentials() },
    {
      onSuccess: () => {
        apiKey.value = hookSecret.value = "";
        toast.success(t("product.integrations.saved", { name: name.value }));
        open.value = false;
      },
    },
  );
}

async function removeNow() {
  if (!(await askConfirm({ body: t("product.integrations.einvoice.confirmRemove", { name: name.value }) }))) return;
  actions.remove.mutate(undefined, { onSuccess: () => (open.value = false) });
}

function test() {
  problem.value = "";
  actions.test.mutate(props.taxId, { onSuccess: (r) => (problem.value = r.ok ? "" : (r.problem ?? "refused")) });
}
const tested = computed(() => actions.test.data.value);
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="sm:max-w-xl" data-testid="einvoice-sheet">
      <SheetHeader>
        <SheetTitle>{{ t(here.state === "none" ? "product.integrations.connectTitle" : "product.integrations.manageTitle", { name }) }}</SheetTitle>
        <SheetDescription>{{ t(`product.integrations.einvoice.intro.${provider || "nilvera"}`) }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <p v-if="!isOwner" class="text-muted-foreground text-sm" data-testid="integration-owner-only">{{ t("product.integrations.ownerOnly") }}</p>
        <p class="bg-muted text-muted-foreground rounded-xl px-3 py-2 text-xs">{{ t("product.integrations.einvoice.envNote") }}</p>
        <p v-if="other" class="text-warning text-xs" role="status" data-testid="einvoice-replaces">{{ t("product.integrations.einvoice.replaces", { current: account?.provider }) }}</p>

        <div v-if="here.state !== 'none'" class="bg-muted flex flex-wrap items-center justify-between gap-3 rounded-2xl px-4 py-3" data-testid="integration-sheet-status">
          <div class="min-w-0">
            <StatusPill :tone="tested && !tested.ok ? 'danger' : 'success'">{{ t(tested && !tested.ok ? "product.integrations.state.problem" : "product.integrations.state.working") }}</StatusPill>
            <p class="text-muted-foreground mt-1 text-xs">{{ t("product.integrations.einvoice.modeLine", { mode: t(`product.integrations.einvoice.modes.${here.mode || "production"}`) }) }}</p>
            <p v-if="tested" class="mt-1 text-xs" role="status" data-testid="integration-test-result" :class="tested.ok ? 'text-success' : 'text-destructive'">
              {{ tested.ok ? t(`product.integrations.einvoice.testOk.${tested.checked ?? "credentials"}`) : t(`product.integrations.testFailed.${problem || "refused"}`) }}
            </p>
          </div>
          <Button v-if="isOwner" variant="outline" size="sm" :disabled="actions.test.isPending.value" data-testid="integration-test" @click="test">{{ actions.test.isPending.value ? t("product.integrations.testing") : t("product.integrations.test") }}</Button>
        </div>

        <form id="einvoice-sheet-form" class="grid gap-3" novalidate autocomplete="off" :aria-label="t('product.integrations.connectTitle', { name })" @submit.prevent="submit">
          <p class="text-muted-foreground text-xs">{{ here.state !== "none" ? t("product.integrations.replaceNote") : t("product.integrations.secretsOnce") }}</p>
          <div class="grid gap-1">
            <label class="text-sm" for="ei-mode">{{ t("product.integrations.einvoice.mode") }}</label>
            <NativeSelect id="ei-mode" v-model="mode" data-testid="ei-mode">
              <option value="production">{{ t("product.integrations.einvoice.modes.production") }}</option>
              <option value="test">{{ t("product.integrations.einvoice.modes.test") }}</option>
            </NativeSelect>
          </div>
          <template v-if="provider === 'nilvera'">
            <div class="grid gap-1">
              <label class="text-sm" for="ei-key">{{ t("product.integrations.einvoice.fields.api_key") }}</label>
              <Input id="ei-key" v-model="apiKey" type="password" class="mono" spellcheck="false" autocomplete="new-password" data-testid="ei-api-key" />
            </div>
            <div class="grid gap-1">
              <label class="text-sm" for="ei-base">{{ t("product.integrations.einvoice.fields.base_url") }} <span class="text-muted-foreground">({{ t("product.integrations.optional") }})</span></label>
              <Input id="ei-base" v-model="baseUrl" type="url" spellcheck="false" data-testid="ei-base-url" />
            </div>
          </template>
          <template v-else-if="provider === 'webhook'">
            <div class="grid gap-1">
              <label class="text-sm" for="ei-url">{{ t("product.integrations.einvoice.fields.url") }}</label>
              <Input id="ei-url" v-model="hookUrl" type="url" spellcheck="false" data-testid="ei-url" />
            </div>
            <div class="grid gap-1">
              <label class="text-sm" for="ei-secret">{{ t("product.integrations.einvoice.fields.secret") }}</label>
              <Input id="ei-secret" v-model="hookSecret" type="password" class="mono" spellcheck="false" autocomplete="new-password" data-testid="ei-secret" />
            </div>
          </template>
        </form>
        <ErrorText :error="actions.save.error.value ?? actions.remove.error.value ?? actions.test.error.value" />
      </SheetBody>
      <SheetFooter>
        <Button v-if="isOwner && here.state !== 'none'" variant="ghost" class="text-destructive me-auto" type="button" :disabled="actions.remove.isPending.value" data-testid="integration-disconnect" @click="removeNow">{{ t("product.integrations.disconnect") }}</Button>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="einvoice-sheet-form" :disabled="!isOwner || actions.save.isPending.value" data-testid="integration-connect">{{ t("product.integrations.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
