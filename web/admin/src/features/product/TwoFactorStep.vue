<script setup lang="ts">
import { nextTick, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import OtpInput from "./OtpInput.vue";
import { useVerifyTwoFactor } from "./twoFactorApi";

/**
 * The second step of a sign-in for a person who has two-factor sign-in on: the six digits of the authenticator app, or a recovery code, together with the
 * challenge the first step handed over. A right code starts the session like any other sign-in (`done`); a wrong one stays on the step with the reason and
 * empty boxes. A challenge that ended or was used up is refused with the same words as a wrong code, so "Start again" (`restart`) is always offered.
 */
const props = defineProps<{ challenge: string }>();
const emit = defineEmits<{ done: []; restart: [] }>();
const { t } = useI18n();
const verify = useVerifyTwoFactor();

const mode = ref<"code" | "recovery">("code");
const code = ref("");
const recovery = ref("");
const otp = ref<InstanceType<typeof OtpInput> | null>(null);
const recoveryBox = ref<HTMLElement | null>(null);

function focus(): void {
  void nextTick(() => (mode.value === "code" ? otp.value?.focusFirst() : recoveryBox.value?.querySelector("input")?.focus()));
}
onMounted(focus);

function submit(value: string): void {
  const typed = value.trim();
  if (typed === "" || verify.isPending.value) return;
  verify.mutate(
    { challenge: props.challenge, code: typed },
    {
      onSuccess: () => emit("done"),
      onError: () => {
        code.value = "";
        recovery.value = "";
        focus();
      },
    },
  );
}

function switchMode(): void {
  mode.value = mode.value === "code" ? "recovery" : "code";
  code.value = "";
  recovery.value = "";
  verify.reset();
  focus();
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="two-factor-step">
    <div>
      <h2 class="text-lg font-semibold tracking-tight">{{ t("product.twoFactor.step.title") }}</h2>
      <p class="text-muted-foreground mt-1 text-sm">{{ mode === "code" ? t("product.twoFactor.step.intro") : t("product.twoFactor.step.recoveryIntro") }}</p>
    </div>
    <form v-if="mode === 'code'" class="flex flex-col gap-4" :aria-label="t('product.twoFactor.step.title')" @submit.prevent="submit(code)">
      <OtpInput ref="otp" v-model="code" :disabled="verify.isPending.value" :invalid="verify.isError.value" @complete="submit" />
      <ErrorText :error="verify.error.value" box />
      <Button type="submit" size="lg" :disabled="code.length !== 6 || verify.isPending.value">{{ t("product.twoFactor.step.submit") }}</Button>
    </form>
    <form v-else ref="recoveryBox" class="flex flex-col gap-4" :aria-label="t('product.twoFactor.step.title')" @submit.prevent="submit(recovery)">
      <FormField for="tf-recovery" :label="t('product.twoFactor.step.recoveryLabel')" :hint="t('product.twoFactor.step.recoveryHint')">
        <Input id="tf-recovery" v-model="recovery" class="mono" autocomplete="off" autocapitalize="none" spellcheck="false" :aria-invalid="verify.isError.value ? 'true' : undefined" />
      </FormField>
      <ErrorText :error="verify.error.value" box />
      <Button type="submit" size="lg" :disabled="recovery.trim() === '' || verify.isPending.value">{{ t("product.twoFactor.step.submit") }}</Button>
    </form>
    <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
      <button type="button" class="text-primary font-medium underline-offset-4 hover:underline" data-testid="two-factor-switch" @click="switchMode">
        {{ mode === "code" ? t("product.twoFactor.step.useRecovery") : t("product.twoFactor.step.useApp") }}
      </button>
      <button type="button" class="text-muted-foreground hover:text-foreground underline-offset-4 hover:underline" data-testid="two-factor-restart" @click="emit('restart')">{{ t("product.twoFactor.step.restart") }}</button>
    </div>
  </div>
</template>
