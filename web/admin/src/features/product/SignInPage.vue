<script setup lang="ts">
import { Link, useNavigate, useSearch } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import { useAuthMethods, useSignIn } from "./api";
import { OAUTH_ERRORS } from "./lib";
import AuthShell from "./AuthShell.vue";
import GoogleButton from "./GoogleButton.vue";
import OtpFlow from "./OtpFlow.vue";
import PasswordField from "./PasswordField.vue";

// The three ways to sign in: e-mail and password, a one-time code mailed to the address, and Google (only when the deployment has set it up).
const { t } = useI18n();
const errorText = useErrorText();
const navigate = useNavigate();
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
  login.mutate({ email: email.value.trim(), password: password.value }, { onSuccess: () => navigate({ to: "/app" }) });
}
</script>

<template>
  <AuthShell :title="t('product.signIn.title')" :intro="t('product.signIn.intro')">
    <p v-if="expired" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status">{{ t("product.signIn.expired") }}</p>
    <p v-if="oauthError" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert" data-testid="oauth-error">
      {{ t(`product.signIn.oauthError.${oauthError}`) }}
    </p>
    <div class="flex flex-col gap-4">
      <template v-if="methods.data.value?.google">
        <GoogleButton />
        <div class="text-muted-foreground flex items-center gap-3 text-xs" aria-hidden="true">
          <span class="bg-border h-px flex-1" />{{ t("product.signIn.or") }}<span class="bg-border h-px flex-1" />
        </div>
      </template>
      <Tabs v-model="method">
        <TabsList class="w-full" :aria-label="t('product.signIn.methods')">
          <TabsTrigger value="password">{{ t("product.signIn.tabPassword") }}</TabsTrigger>
          <TabsTrigger value="code">{{ t("product.signIn.tabCode") }}</TabsTrigger>
        </TabsList>
      </Tabs>
      <form v-if="method === 'password'" class="flex flex-col gap-4" :aria-label="t('product.signIn.title')" @submit.prevent="submit">
        <div class="flex flex-col gap-1.5">
          <Label for="si-email">{{ t("product.auth.email") }}</Label>
          <Input id="si-email" v-model="email" type="email" autocomplete="username" inputmode="email" required />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="si-password">{{ t("product.auth.password") }}</Label>
          <PasswordField id="si-password" v-model="password" autocomplete="current-password" />
          <Link to="/forgot" class="text-muted-foreground hover:text-foreground self-end text-xs underline-offset-4 hover:underline">{{ t("product.signIn.forgot") }}</Link>
        </div>
        <p v-if="login.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(login.error.value) }}</p>
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
