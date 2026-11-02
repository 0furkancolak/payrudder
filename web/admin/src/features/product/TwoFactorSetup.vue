<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { CopyField } from "@/shared/ui/copy-field";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import OtpInput from "./OtpInput.vue";
import QrCode from "./QrCode.vue";
import RecoveryCodes from "./RecoveryCodes.vue";
import { useConfirmTwoFactor, useSetupTwoFactor } from "./twoFactorApi";

/**
 * Turning two-factor sign-in on, in three steps: scan the QR code (or type the key) into an authenticator app, prove it with the first six-digit code, then
 * keep the ten recovery codes. Closing before the last step throws the secret away (the next try makes a new one); once the codes are shown the dialog can
 * only be closed with "Done", after the person says they saved them. The secret and the codes live in the two mutations of this dialog (`gcTime: 0`),
 * which are reset when it closes.
 */
defineProps<{ email: string }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const setup = useSetupTwoFactor();
const confirm = useConfirmTwoFactor();
const step = ref<"scan" | "code" | "codes">("scan");
const code = ref("");
const otp = ref<InstanceType<typeof OtpInput> | null>(null);
const codes = computed(() => confirm.data.value?.recovery_codes ?? []);

watch(
  open,
  (o) => {
    code.value = "";
    if (o) {
      step.value = "scan";
      setup.mutate();
    } else {
      setup.reset();
      confirm.reset();
    }
  },
  { immediate: true },
);
watch(step, async (s) => {
  if (s !== "code") return;
  await nextTick();
  void otp.value?.focusFirst();
});

function submit(value = code.value): void {
  if (value.length !== 6 || confirm.isPending.value) return;
  confirm.mutate(value, {
    onSuccess: () => (step.value = "codes"),
    onError: () => {
      code.value = "";
      void nextTick(() => otp.value?.focusFirst());
    },
  });
}

/** Once the codes are shown the dialog stays until "Done". */
function guard(e: Event): void {
  if (step.value === "codes") e.preventDefault();
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-lg" :show-close-button="step !== 'codes'" data-testid="two-factor-setup" @interact-outside="guard" @escape-key-down="guard">
      <DialogHeader>
        <DialogTitle>{{ t(`product.twoFactor.setup.${step}.title`) }}</DialogTitle>
        <DialogDescription>{{ t(`product.twoFactor.setup.${step}.body`) }}</DialogDescription>
      </DialogHeader>

      <template v-if="step === 'scan'">
        <Skeleton v-if="setup.isPending.value" class="h-52 w-52 self-center justify-self-center rounded-xl" />
        <template v-else-if="setup.data.value">
          <div class="flex justify-center"><QrCode :value="setup.data.value.otpauth_uri" :label="t('product.twoFactor.setup.qrLabel')" /></div>
          <p class="text-muted-foreground text-sm">{{ t("product.twoFactor.setup.cantScan") }}</p>
          <CopyField :value="setup.data.value.secret" :label="t('product.twoFactor.setup.secretLabel')" :copy-label="t('product.twoFactor.setup.copySecret')" testid="two-factor-secret" />
          <DialogFooter>
            <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
            <Button type="button" data-testid="two-factor-next" @click="step = 'code'">{{ t("product.twoFactor.setup.next") }}</Button>
          </DialogFooter>
        </template>
        <template v-else>
          <ErrorText :error="setup.error.value" box />
          <DialogFooter>
            <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
            <Button type="button" variant="outline" @click="setup.mutate()">{{ t("errorState.retry") }}</Button>
          </DialogFooter>
        </template>
      </template>

      <form v-else-if="step === 'code'" class="grid gap-4" :aria-label="t('product.twoFactor.setup.code.title')" @submit.prevent="submit()">
        <OtpInput ref="otp" v-model="code" :disabled="confirm.isPending.value" :invalid="confirm.isError.value" @complete="submit" />
        <ErrorText :error="confirm.error.value" box />
        <DialogFooter>
          <Button type="button" variant="ghost" @click="step = 'scan'; confirm.reset()">{{ t("product.twoFactor.setup.back") }}</Button>
          <Button type="submit" :disabled="code.length !== 6 || confirm.isPending.value" data-testid="two-factor-confirm">{{ t("product.twoFactor.setup.turnOn") }}</Button>
        </DialogFooter>
      </form>

      <RecoveryCodes v-else :codes="codes" :email="email" @done="open = false" />
    </DialogContent>
  </Dialog>
</template>
