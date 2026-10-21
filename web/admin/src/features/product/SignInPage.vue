<script setup lang="ts">
import { Link, useNavigate, useSearch } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { useSignIn } from "./api";
import AuthShell from "./AuthShell.vue";
import PasswordField from "./PasswordField.vue";

const { t } = useI18n();
const errorText = useErrorText();
const navigate = useNavigate();
const search = useSearch({ strict: false });
const email = ref("");
const password = ref("");
const login = useSignIn();
const expired = computed(() => (search.value as { expired?: string }).expired === "1");

function submit() {
  login.mutate({ email: email.value.trim(), password: password.value }, { onSuccess: () => navigate({ to: "/app" }) });
}
</script>

<template>
  <AuthShell :title="t('product.signIn.title')" :intro="t('product.signIn.intro')">
    <p v-if="expired" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status">{{ t("product.signIn.expired") }}</p>
    <form class="flex flex-col gap-4" :aria-label="t('product.signIn.title')" @submit.prevent="submit">
      <div class="flex flex-col gap-1.5">
        <Label for="si-email">{{ t("product.auth.email") }}</Label>
        <Input id="si-email" v-model="email" type="email" autocomplete="username" inputmode="email" required />
      </div>
      <div class="flex flex-col gap-1.5">
        <Label for="si-password">{{ t("product.auth.password") }}</Label>
        <PasswordField id="si-password" v-model="password" autocomplete="current-password" />
      </div>
      <p v-if="login.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(login.error.value) }}</p>
      <Button type="submit" size="lg" :disabled="login.isPending.value || email.trim() === '' || password === ''">{{ t("product.signIn.submit") }}</Button>
    </form>
    <template #footer>
      <p>
        {{ t("product.signIn.noAccount") }}
        <Link to="/signup" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("product.signUp.cta") }}</Link>
      </p>
      <p class="mt-1">
        <Link to="/operator" class="underline-offset-4 hover:underline">{{ t("product.signIn.operator") }}</Link>
      </p>
    </template>
  </AuthShell>
</template>
