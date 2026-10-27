<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { takeFragmentToken } from "@/shared/lib/fragment";
import { isPerson } from "@/shared/lib/session";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { useAcceptInvitation, useAcceptInvitationAsMe, useLookupInvitation, useMe, useSignIn, useSignOut } from "./api";
import { PASSWORD_MIN } from "./lib";
import AuthShell from "./AuthShell.vue";
import PasswordField from "./PasswordField.vue";

/**
 * The page an invitation mail opens: /invite#token=… . The token is read from the fragment once. What the page asks for depends on the invitee:
 * somebody without an account chooses a password; somebody with one signs in (or, when already signed in with that address, just joins).
 */
const { t } = useI18n();
const navigate = useNavigate();
const token = takeFragmentToken();
const lookup = useLookupInvitation();
const acceptNew = useAcceptInvitation();
const acceptMe = useAcceptInvitationAsMe();
const login = useSignIn();
const signOut = useSignOut();
const me = useMe();

const info = computed(() => lookup.data.value);
const name = ref("");
const password = ref("");
const longEnough = computed(() => password.value.length >= PASSWORD_MIN);
const missing = token === "";

onMounted(() => {
  if (!missing) lookup.mutate(token);
});

const myAddress = computed(() => me.data.value?.user?.email?.toLowerCase() ?? "");
const signedInAsInvitee = computed(() => isPerson.value && myAddress.value !== "" && myAddress.value === info.value?.email?.toLowerCase());
const signedInAsSomeoneElse = computed(() => isPerson.value && myAddress.value !== "" && !signedInAsInvitee.value);
const error = computed(() => acceptNew.error.value ?? acceptMe.error.value ?? login.error.value);

const toApp = () => navigate({ to: "/app" });
const joinAsMe = () => acceptMe.mutate(token, { onSuccess: toApp });
const joinNew = () => longEnough.value && acceptNew.mutate({ token, password: password.value, displayName: name.value.trim() }, { onSuccess: toApp });
const signInAndJoin = () => login.mutate({ email: info.value?.email ?? "", password: password.value }, { onSuccess: joinAsMe });
</script>

<template>
  <AuthShell :title="t('product.invite.title')" :intro="info ? t('product.invite.intro', { account: info.account_name ?? '', role: t(`product.role.${info.role}`) }) : t('product.invite.checking')">
    <p v-if="missing || lookup.isError.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">
      {{ missing ? t("product.invite.noToken") : t("product.invite.invalid") }}
    </p>
    <p v-else-if="lookup.isPending.value || !info" class="text-muted-foreground text-sm" role="status">{{ t("product.invite.checking") }}</p>

    <template v-else>
      <p v-if="info.invited_by_name" class="text-muted-foreground text-sm">{{ t("product.invite.from", { name: info.invited_by_name }) }}</p>

      <!-- Signed in with the address that was invited: one click. -->
      <div v-if="signedInAsInvitee" class="flex flex-col gap-4">
        <p class="text-sm">{{ t("product.invite.joinAs", { email: info.email ?? '' }) }}</p>
        <ErrorText :error="error" class="bg-destructive-soft rounded-2xl px-4 py-3" />
        <Button size="lg" :disabled="acceptMe.isPending.value" @click="joinAsMe">{{ t("product.invite.join") }}</Button>
      </div>

      <!-- Signed in as somebody else: the invitation is for another address. -->
      <div v-else-if="signedInAsSomeoneElse" class="flex flex-col gap-4">
        <p class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status">{{ t("product.invite.otherUser", { invited: info.email ?? '', current: myAddress }) }}</p>
        <Button size="lg" variant="outline" :disabled="signOut.isPending.value" @click="signOut.mutate()">{{ t("nav.signOut") }}</Button>
      </div>

      <!-- The address has an account: sign in, then join. -->
      <form v-else-if="info.has_account" class="flex flex-col gap-4" :aria-label="t('product.invite.signInTitle')" @submit.prevent="signInAndJoin">
        <p class="text-sm">{{ t("product.invite.hasAccount", { email: info.email ?? '' }) }}</p>
        <div class="flex flex-col gap-1.5">
          <Label for="iv-email">{{ t("product.auth.email") }}</Label>
          <Input id="iv-email" :model-value="info.email" type="email" readonly />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="iv-password">{{ t("product.auth.password") }}</Label>
          <PasswordField id="iv-password" v-model="password" autocomplete="current-password" />
        </div>
        <ErrorText :error="error" class="bg-destructive-soft rounded-2xl px-4 py-3" />
        <Button type="submit" size="lg" :disabled="login.isPending.value || acceptMe.isPending.value || password === ''">{{ t("product.invite.signInJoin") }}</Button>
      </form>

      <!-- A new person: a name and a password, and they are in. -->
      <form v-else class="flex flex-col gap-4" :aria-label="t('product.invite.createTitle')" @submit.prevent="joinNew">
        <div class="flex flex-col gap-1.5">
          <Label for="iv-new-email">{{ t("product.auth.email") }}</Label>
          <Input id="iv-new-email" :model-value="info.email" type="email" readonly />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="iv-name">{{ t("product.auth.name") }}</Label>
          <Input id="iv-name" v-model="name" autocomplete="name" />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="iv-new-password">{{ t("product.auth.password") }}</Label>
          <PasswordField id="iv-new-password" v-model="password" autocomplete="new-password" describedby="iv-password-hint" />
          <p id="iv-password-hint" :class="['text-xs', password === '' || longEnough ? 'text-muted-foreground' : 'text-destructive']">{{ t("product.auth.passwordHint", { min: PASSWORD_MIN }) }}</p>
        </div>
        <ErrorText :error="error" class="bg-destructive-soft rounded-2xl px-4 py-3" />
        <Button type="submit" size="lg" :disabled="acceptNew.isPending.value || !longEnough">{{ t("product.invite.createJoin") }}</Button>
      </form>
    </template>
  </AuthShell>
</template>
