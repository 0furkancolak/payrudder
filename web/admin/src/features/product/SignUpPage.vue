<script setup lang="ts">
import { Link, useNavigate } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { FormField } from "@/shared/ui/form-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { useAuthMethods, useSignUp } from "./api";
import { PASSWORD_MIN } from "./lib";
import AuthShell from "./AuthShell.vue";
import AuthMethodPicker from "./AuthMethodPicker.vue";
import OtpFlow from "./OtpFlow.vue";
import PasswordField from "./PasswordField.vue";

const { t } = useI18n();
const navigate = useNavigate();
const name = ref("");
const account = ref("");
const email = ref("");
const password = ref("");
const signup = useSignUp();
const methods = useAuthMethods();
/** The same three ways as signing in: a password chosen now, a one-time code (which creates the account when it is entered), or Google. */
const method = ref("password");
/** Set when the sign-in right after sign-up did not work: the same "check your inbox" for a taken address as for a new one that could not be signed in. */
const checkInbox = ref("");

/** The same rule the server applies: at least ten characters. */
const longEnough = computed(() => password.value.length >= PASSWORD_MIN);
const ready = computed(() => account.value.trim() !== "" && email.value.trim() !== "" && longEnough.value);

function submit() {
  if (!ready.value) return;
  signup.mutate(
    { email: email.value.trim(), password: password.value, display_name: name.value.trim(), account_name: account.value.trim() },
    {
      onSuccess: (r) => {
        if (r.signedIn) void navigate({ to: "/app" });
        else checkInbox.value = email.value.trim();
      },
    },
  );
}
</script>

<template>
  <AuthShell :title="t('product.signUp.title')" :intro="t('product.signUp.intro')">
    <div v-if="!checkInbox" class="flex flex-col gap-4">
      <AuthMethodPicker v-model="method" :google="methods.data.value?.google" />
    </div>
    <div v-if="checkInbox" class="bg-success-soft text-success flex flex-col gap-1 rounded-2xl px-4 py-3 text-sm" role="status">
      <p class="font-medium">{{ t("product.signUp.checkTitle") }}</p>
      <p>{{ t("product.signUp.checkBody", { email: checkInbox }) }}</p>
    </div>
    <OtpFlow v-else-if="method === 'code'" mode="signup" />
    <form v-else class="flex flex-col gap-4" :aria-label="t('product.signUp.title')" @submit.prevent="submit">
      <div class="grid gap-4 sm:grid-cols-2">
        <FormField for="su-name" :label="t('product.auth.name')">
          <Input id="su-name" v-model="name" autocomplete="name" />
        </FormField>
        <FormField for="su-account" :label="t('product.auth.organisation')">
          <Input id="su-account" v-model="account" autocomplete="organization" required />
        </FormField>
      </div>
      <FormField for="su-email" :label="t('product.auth.email')">
        <Input id="su-email" v-model="email" type="email" autocomplete="username" inputmode="email" required />
      </FormField>
      <FormField for="su-password" :label="t('product.auth.password')">
        <PasswordField id="su-password" v-model="password" autocomplete="new-password" describedby="su-password-hint" />
        <p id="su-password-hint" :class="['text-xs', password === '' || longEnough ? 'text-muted-foreground' : 'text-destructive']">{{ t("product.auth.passwordHint", { min: PASSWORD_MIN }) }}</p>
      </FormField>
      <ErrorText :error="signup.error.value" box />
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
