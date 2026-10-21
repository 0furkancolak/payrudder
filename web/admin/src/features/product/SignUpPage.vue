<script setup lang="ts">
import { Link, useNavigate } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { useSignUp } from "./api";
import AuthShell from "./AuthShell.vue";
import PasswordField from "./PasswordField.vue";

const { t } = useI18n();
const errorText = useErrorText();
const navigate = useNavigate();
const name = ref("");
const account = ref("");
const email = ref("");
const password = ref("");
const signup = useSignUp();

/** The same rule the server applies: at least ten characters. */
const MIN = 10;
const longEnough = computed(() => password.value.length >= MIN);
const ready = computed(() => account.value.trim() !== "" && email.value.trim() !== "" && longEnough.value);

function submit() {
  if (!ready.value) return;
  signup.mutate(
    { email: email.value.trim(), password: password.value, display_name: name.value.trim(), account_name: account.value.trim() },
    { onSuccess: () => navigate({ to: "/app" }) },
  );
}
</script>

<template>
  <AuthShell :title="t('product.signUp.title')" :intro="t('product.signUp.intro')">
    <form class="flex flex-col gap-4" :aria-label="t('product.signUp.title')" @submit.prevent="submit">
      <div class="grid gap-4 sm:grid-cols-2">
        <div class="flex flex-col gap-1.5">
          <Label for="su-name">{{ t("product.auth.name") }}</Label>
          <Input id="su-name" v-model="name" autocomplete="name" />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="su-account">{{ t("product.auth.organisation") }}</Label>
          <Input id="su-account" v-model="account" autocomplete="organization" required />
        </div>
      </div>
      <div class="flex flex-col gap-1.5">
        <Label for="su-email">{{ t("product.auth.email") }}</Label>
        <Input id="su-email" v-model="email" type="email" autocomplete="username" inputmode="email" required />
      </div>
      <div class="flex flex-col gap-1.5">
        <Label for="su-password">{{ t("product.auth.password") }}</Label>
        <PasswordField id="su-password" v-model="password" autocomplete="new-password" describedby="su-password-hint" />
        <p id="su-password-hint" :class="['text-xs', password === '' || longEnough ? 'text-muted-foreground' : 'text-destructive']">{{ t("product.auth.passwordHint", { min: MIN }) }}</p>
      </div>
      <p v-if="signup.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(signup.error.value) }}</p>
      <Button type="submit" size="lg" :disabled="signup.isPending.value || !ready">{{ t("product.signUp.submit") }}</Button>
    </form>
    <template #footer>
      <p>
        {{ t("product.signUp.haveAccount") }}
        <Link to="/signin" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("product.signIn.cta") }}</Link>
      </p>
    </template>
  </AuthShell>
</template>
