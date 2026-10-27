<script setup lang="ts">
import { MailWarning } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { useMe, useResendVerification } from "./api";

/**
 * "Confirm your e-mail address": shown on every page of the signed-in product until the address is verified. A person may use the app meanwhile; what
 * waits for the proof is creating live API keys (the server refuses them, and the keys panel says why).
 */
const { t } = useI18n();
const errorText = useErrorText();
const me = useMe();
const resend = useResendVerification();
const show = computed(() => me.data.value?.user != null && me.data.value.user.email_verified === false);
</script>

<template>
  <div v-if="show" class="bg-warning-soft text-warning mb-6 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="verify-banner">
    <MailWarning class="size-4 shrink-0" aria-hidden="true" />
    <p class="min-w-0 flex-1 basis-60">{{ t("product.verify.banner", { email: me.data.value?.user?.email ?? "" }) }}</p>
    <p v-if="resend.isSuccess.value" class="font-medium">{{ t("product.verify.sent") }}</p>
    <p v-else-if="resend.error.value" class="font-medium" role="alert">{{ errorText(resend.error.value) }}</p>
    <Button v-if="!resend.isSuccess.value" variant="outline" size="sm" :disabled="resend.isPending.value" @click="resend.mutate()">{{ t("product.verify.resend") }}</Button>
  </div>
</template>
