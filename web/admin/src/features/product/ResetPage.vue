<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { takeFragmentToken } from "@/shared/lib/fragment";
import { Button } from "@/shared/ui/button";
import { Label } from "@/shared/ui/label";
import { useResetPassword } from "./api";
import AuthShell from "./AuthShell.vue";
import PasswordField from "./PasswordField.vue";

/** The page the reset mail opens: /reset#token=… . The token is read from the fragment once and kept only in this component. */
const { t } = useI18n();
const errorText = useErrorText();
const token = takeFragmentToken();
const password = ref("");
const done = ref(false);
const reset = useResetPassword();

const MIN = 10;
const longEnough = computed(() => password.value.length >= MIN);

function submit() {
  if (!longEnough.value) return;
  reset.mutate({ token, password: password.value }, { onSuccess: () => (done.value = true) });
}
</script>

<template>
  <AuthShell :title="t('product.reset.title')" :intro="t('product.reset.intro')">
    <div v-if="done" class="bg-success-soft text-success flex flex-col gap-1 rounded-2xl px-4 py-3 text-sm" role="status">
      <p class="font-medium">{{ t("product.reset.doneTitle") }}</p>
      <p>{{ t("product.reset.doneBody") }}</p>
    </div>
    <p v-else-if="token === ''" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="alert">{{ t("product.reset.noToken") }}</p>
    <form v-else class="flex flex-col gap-4" :aria-label="t('product.reset.title')" @submit.prevent="submit">
      <div class="flex flex-col gap-1.5">
        <Label for="rp-password">{{ t("product.reset.newPassword") }}</Label>
        <PasswordField id="rp-password" v-model="password" autocomplete="new-password" describedby="rp-password-hint" />
        <p id="rp-password-hint" :class="['text-xs', password === '' || longEnough ? 'text-muted-foreground' : 'text-destructive']">{{ t("product.auth.passwordHint", { min: MIN }) }}</p>
      </div>
      <p v-if="reset.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(reset.error.value) }}</p>
      <Button type="submit" size="lg" :disabled="reset.isPending.value || !longEnough">{{ t("product.reset.submit") }}</Button>
    </form>
    <template #footer>
      <Link v-if="done" to="/signin" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("product.signIn.cta") }}</Link>
      <Link v-else-if="reset.error.value || token === ''" to="/forgot" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("product.reset.askAgain") }}</Link>
    </template>
  </AuthShell>
</template>
