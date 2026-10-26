<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import type { Environment } from "@/shared/lib/environment";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useEInvoiceAccountActions } from "./api";

type Provider = "mock" | "nilvera" | "webhook";

/** Adds the integrator account of an environment, or replaces it. The credentials are sent once and never read back. */
const props = defineProps<{ tenantId: string }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const actions = useEInvoiceAccountActions(() => props.tenantId);

const environment = ref<Environment>("live");
const provider = ref<Provider>("nilvera");
const mode = ref<"test" | "production">("test");
const apiKey = ref("");
const baseUrl = ref("");
const hookUrl = ref("");
const hookSecret = ref("");
/** The sandbox only reaches the mock; a live account may be any integrator. */
const providers = computed<Provider[]>(() => (environment.value === "sandbox" ? ["mock"] : ["nilvera", "webhook", "mock"]));
watch(environment, (e) => {
  if (e === "sandbox") provider.value = "mock";
  else if (provider.value === "mock") provider.value = "nilvera";
});
watch(open, (isOpen) => {
  if (!isOpen) return;
  environment.value = "live";
  provider.value = "nilvera";
  mode.value = "test";
  apiKey.value = baseUrl.value = hookUrl.value = hookSecret.value = "";
  actions.save.reset();
});

function credentials(): Record<string, string> {
  if (provider.value === "nilvera") return { api_key: apiKey.value.trim(), ...(baseUrl.value.trim() ? { base_url: baseUrl.value.trim() } : {}) };
  if (provider.value === "webhook") return { url: hookUrl.value.trim(), secret: hookSecret.value };
  return {};
}
function saveAccount() {
  actions.save.mutate(
    { environment: environment.value, provider: provider.value, mode: mode.value, credentials: credentials() },
    {
      onSuccess: () => {
        toast.success(t("einvoice.account.saved"));
        open.value = false;
      },
    },
  );
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("einvoice.account.add") }}</SheetTitle>
        <SheetDescription>{{ t("einvoice.account.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="einvoice-account-form" class="grid gap-3" role="form" :aria-label="t('einvoice.account.formLabel')" @submit.prevent="saveAccount">
          <NativeSelect v-model="environment" :aria-label="t('einvoice.account.environment')">
            <option value="live">live</option>
            <option value="sandbox">sandbox</option>
          </NativeSelect>
          <NativeSelect v-model="provider" :aria-label="t('einvoice.account.provider')">
            <option v-for="p in providers" :key="p" :value="p">{{ p }}</option>
          </NativeSelect>
          <NativeSelect v-model="mode" :aria-label="t('einvoice.account.mode')">
            <option value="test">{{ t("einvoice.account.modes.test") }}</option>
            <option value="production">{{ t("einvoice.account.modes.production") }}</option>
          </NativeSelect>
          <p v-if="environment === 'sandbox'" class="text-muted-foreground text-xs">{{ t("einvoice.account.sandboxHint") }}</p>
          <template v-if="provider === 'nilvera'">
            <Input v-model="apiKey" type="password" autocomplete="off" :aria-label="t('einvoice.account.apiKey')" :placeholder="t('einvoice.account.apiKey')" required />
            <Input v-model="baseUrl" type="url" :aria-label="t('einvoice.account.baseUrl')" :placeholder="t('einvoice.account.baseUrl')" />
          </template>
          <template v-else-if="provider === 'webhook'">
            <Input v-model="hookUrl" type="url" :aria-label="t('einvoice.account.webhookUrl')" :placeholder="t('einvoice.account.webhookUrl')" required />
            <Input v-model="hookSecret" type="password" autocomplete="off" :aria-label="t('einvoice.account.webhookSecret')" :placeholder="t('einvoice.account.webhookSecret')" required />
          </template>
          <p v-if="provider !== 'mock'" class="text-muted-foreground text-xs">{{ t("einvoice.account.neverShown") }}</p>
          <ErrorText :error="actions.save.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="outline" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="einvoice-account-form" :disabled="actions.save.isPending.value">{{ t("einvoice.account.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
