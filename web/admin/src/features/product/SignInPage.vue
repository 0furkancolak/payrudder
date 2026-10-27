<script setup lang="ts">
import { Link, useSearch } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { FormField } from "@/shared/ui/form-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { useAuthMethods, useSignIn } from "./api";
import { useAfterSignIn } from "./afterSignIn";
import { OAUTH_ERRORS } from "./lib";
import AuthShell from "./AuthShell.vue";
import AuthMethodPicker from "./AuthMethodPicker.vue";
import OtpFlow from "./OtpFlow.vue";
import PasswordField from "./PasswordField.vue";

// The three ways to sign in: e-mail and password, a one-time code mailed to the address, and Google (only when the deployment has set it up).
const { t } = useI18n();
const afterSignIn = useAfterSignIn();
const search = useSearch({ strict: false });
const methods = useAuthMethods();
const email = ref("");
const password = ref("");
const method = ref("password");
const login = useSignIn();
// Local development only (stripped from production builds): the public credentials of the default dev seed (`make dev` creates them).
const devHint = import.meta.env.DEV;
const expired = computed(() => (search.value as { expired?: string }).expired === "1");
/** Why a login with Google ended without a session, if it did. */
const oauthError = computed(() => {
  const v = (search.value as { oauth_error?: string }).oauth_error ?? "";
  return OAUTH_ERRORS.includes(v) ? v : "";
});

function submit() {
  login.mutate({ email: email.value.trim(), password: password.value }, { onSuccess: () => afterSignIn() });
}
</script>

<template>
  <AuthShell :title="t('product.signIn.title')" :intro="t('product.signIn.intro')">
    <p v-if="expired" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status">{{ t("product.signIn.expired") }}</p>
    <p v-if="oauthError" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert" data-testid="oauth-error">
      {{ t(`product.signIn.oauthError.${oauthError}`) }}
    </p>
    <div class="flex flex-col gap-4">
      <AuthMethodPicker v-model="method" :google="methods.data.value?.google" />
      <form v-if="method === 'password'" class="flex flex-col gap-4" :aria-label="t('product.signIn.title')" @submit.prevent="submit">
        <FormField for="si-email" :label="t('product.auth.email')">
          <Input id="si-email" v-model="email" type="email" autocomplete="username" inputmode="email" required />
        </FormField>
        <FormField for="si-password" :label="t('product.auth.password')">
          <PasswordField id="si-password" v-model="password" autocomplete="current-password" />
          <Link to="/forgot" class="text-muted-foreground hover:text-foreground self-end text-xs underline-offset-4 hover:underline">{{ t("product.signIn.forgot") }}</Link>
        </FormField>
        <ErrorText :error="login.error.value" box />
        <Button type="submit" size="lg" :disabled="login.isPending.value || email.trim() === '' || password === ''">{{ t("product.signIn.submit") }}</Button>
      </form>
      <OtpFlow v-else mode="signin" />
    </div>
    <template #footer>
      <p v-if="devHint" class="bg-muted text-muted-foreground mb-3 rounded-2xl px-4 py-3 text-xs" data-testid="dev-seed-hint">
        Dev seed: <span class="mono">user1@example.com</span> / <span class="mono">password123</span>
      </p>
      <p>
        {{ t("product.signIn.noAccount") }}
        <Link to="/signup" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("product.signUp.cta") }}</Link>
      </p>
    </template>
  </AuthShell>
</template>
