<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { FormField } from "@/shared/ui/form-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { useForgotPassword } from "./api";
import AuthShell from "./AuthShell.vue";

/** "Forgot your password": the answer is the same for every address, and so is what this page says. */
const { t } = useI18n();
const email = ref("");
const sentTo = ref("");
const forgot = useForgotPassword();

function submit() {
  const address = email.value.trim();
  forgot.mutate(address, { onSuccess: () => (sentTo.value = address) });
}
</script>

<template>
  <AuthShell :title="t('product.forgot.title')" :intro="t('product.forgot.intro')">
    <div v-if="sentTo" class="bg-success-soft text-success flex flex-col gap-1 rounded-2xl px-4 py-3 text-sm" role="status">
      <p class="font-medium">{{ t("product.forgot.doneTitle") }}</p>
      <p>{{ t("product.forgot.doneBody", { email: sentTo }) }}</p>
    </div>
    <form v-else class="flex flex-col gap-4" :aria-label="t('product.forgot.title')" @submit.prevent="submit">
      <FormField for="fp-email" :label="t('product.auth.email')">
        <Input id="fp-email" v-model="email" type="email" autocomplete="username" inputmode="email" required />
      </FormField>
      <ErrorText :error="forgot.error.value" box />
      <Button type="submit" size="lg" :disabled="forgot.isPending.value || email.trim() === ''">{{ t("product.forgot.submit") }}</Button>
    </form>
    <template #footer>
      <Link to="/signin" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("product.forgot.back") }}</Link>
    </template>
  </AuthShell>
</template>
