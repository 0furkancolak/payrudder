<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { computed, nextTick, onBeforeUnmount, ref } from "vue";
import { useI18n } from "vue-i18n";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { useRequestCode, useVerifyCode } from "./api";
import OtpInput from "./OtpInput.vue";

/**
 * Signing in (or up) with a one-time code: the address, then the six digits mailed to it. The same answer comes back for every address, so the page says
 * "we sent a code" without knowing whether there is an account; entering the right code signs in, and for an address without an account it also creates one
 * (that is the sign-up with a code). The code can be asked for again once a minute, which the countdown shows.
 */
const props = defineProps<{ mode: "signin" | "signup" }>();
const { t } = useI18n();
const navigate = useNavigate();
const request = useRequestCode();
const verify = useVerifyCode();

/** Seconds between two codes to one address: the server's rule, shown so the person is not left guessing. */
const RESEND_AFTER = 60;

const step = ref<"email" | "code">("email");
const name = ref("");
const organisation = ref("");
const email = ref("");
const code = ref("");
const left = ref(0);
const resent = ref(false);
const otp = ref<InstanceType<typeof OtpInput> | null>(null);
let timer: ReturnType<typeof setInterval> | undefined;

function startCountdown(): void {
  left.value = RESEND_AFTER;
  clearInterval(timer);
  timer = setInterval(() => {
    left.value -= 1;
    if (left.value <= 0) clearInterval(timer);
  }, 1000);
}
onBeforeUnmount(() => clearInterval(timer));

const canSend = computed(() => email.value.trim() !== "" && !request.isPending.value);

function send(): void {
  if (!canSend.value) return;
  verify.reset();
  request.mutate(email.value.trim(), {
    onSuccess: async () => {
      step.value = "code";
      code.value = "";
      resent.value = false;
      startCountdown();
      await nextTick();
      void otp.value?.focusFirst();
    },
  });
}

function resend(): void {
  if (left.value > 0 || request.isPending.value) return;
  verify.reset();
  request.mutate(email.value.trim(), {
    onSuccess: () => {
      code.value = "";
      resent.value = true;
      startCountdown();
      void otp.value?.focusFirst();
    },
  });
}

function submit(value = code.value): void {
  if (value.length !== 6 || verify.isPending.value) return;
  resent.value = false;
  verify.mutate(
    { email: email.value.trim(), code: value, display_name: name.value.trim() || undefined, account_name: organisation.value.trim() || undefined },
    {
      onSuccess: () => void navigate({ to: "/app" }),
      onError: () => {
        code.value = "";
        void nextTick(() => otp.value?.focusFirst());
      },
    },
  );
}

function change(): void {
  clearInterval(timer);
  step.value = "email";
  code.value = "";
  verify.reset();
  request.reset();
}
</script>

<template>
  <form v-if="step === 'email'" class="flex flex-col gap-4" :aria-label="t('product.signIn.code.formLabel')" @submit.prevent="send">
    <p class="text-muted-foreground text-sm">{{ props.mode === "signup" ? t("product.signUp.code.intro") : t("product.signIn.code.intro") }}</p>
    <div v-if="props.mode === 'signup'" class="grid gap-4 sm:grid-cols-2">
      <div class="flex flex-col gap-1.5">
        <Label for="otp-name">{{ t("product.auth.name") }}</Label>
        <Input id="otp-name" v-model="name" autocomplete="name" />
      </div>
      <div class="flex flex-col gap-1.5">
        <Label for="otp-org">{{ t("product.signUp.code.organisation") }}</Label>
        <Input id="otp-org" v-model="organisation" autocomplete="organization" />
      </div>
    </div>
    <div class="flex flex-col gap-1.5">
      <Label for="otp-email">{{ t("product.auth.email") }}</Label>
      <Input id="otp-email" v-model="email" type="email" autocomplete="username" inputmode="email" required />
    </div>
    <ErrorText :error="request.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />
    <Button type="submit" size="lg" :disabled="!canSend">{{ t("product.signIn.code.send") }}</Button>
  </form>

  <form v-else class="flex flex-col gap-4" :aria-label="t('product.signIn.code.verifyLabel')" @submit.prevent="submit()">
    <p class="text-muted-foreground text-sm" role="status">{{ t("product.signIn.code.sentTo", { email: email.trim() }) }}</p>
    <OtpInput ref="otp" v-model="code" :disabled="verify.isPending.value" :invalid="verify.isError.value" @complete="submit" />
    <ErrorText v-if="verify.error.value" :error="verify.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />
    <p v-else-if="resent" class="bg-success-soft text-success rounded-2xl px-4 py-3 text-sm" role="status">{{ t("product.signIn.code.resent") }}</p>
    <Button type="submit" size="lg" :disabled="code.length !== 6 || verify.isPending.value">
      {{ props.mode === "signup" ? t("product.signUp.submit") : t("product.signIn.submit") }}
    </Button>
    <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
      <button
        type="button"
        class="text-primary font-medium underline-offset-4 hover:underline disabled:text-muted-foreground disabled:no-underline"
        :disabled="left > 0 || request.isPending.value"
        @click="resend"
      >
        {{ left > 0 ? t("product.signIn.code.resendIn", { seconds: left }) : t("product.signIn.code.resend") }}
      </button>
      <button type="button" class="text-muted-foreground hover:text-foreground underline-offset-4 hover:underline" @click="change">{{ t("product.signIn.code.change") }}</button>
    </div>
  </form>
</template>
