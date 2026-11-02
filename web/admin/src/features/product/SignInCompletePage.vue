<script setup lang="ts">
import { useQueryClient } from "@tanstack/vue-query";
import { useNavigate } from "@tanstack/vue-router";
import { onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { api, unwrap } from "@/shared/api/client";
import { takeFragmentChallenge, takeFragmentToken } from "@/shared/lib/fragment";
import { setOperatorFlag, setPersonToken } from "@/shared/lib/session";
import AuthShell from "./AuthShell.vue";
import TwoFactorStep from "./TwoFactorStep.vue";

/**
 * Where the gateway sends the browser after "Continue with Google". The session token is in the URL fragment (never sent to a server, so in no log): it is
 * read once, removed from the address bar, kept for this tab, and the person is taken home. A person with two-factor sign-in on gets `#challenge=` instead
 * (read the same way): the second step is shown here and a right code opens the session. A visit with neither goes back to the sign-in page.
 */
const { t } = useI18n();
const navigate = useNavigate();
const queries = useQueryClient();

const challenge = ref("");

onMounted(async () => {
  const second = takeFragmentChallenge();
  if (second) {
    challenge.value = second.challenge;
    return;
  }
  const token = takeFragmentToken();
  if (!token.startsWith("qs_")) {
    await navigate({ to: "/signin", search: { oauth_error: "failed" } as never });
    return;
  }
  queries.clear();
  setPersonToken(token);
  try {
    // The server's verdict on whether this person is an operator (the fragment carries only the token).
    const me = await unwrap(api.GET("/auth/me"));
    setOperatorFlag(me.operator === true);
  } catch {
    // The session is checked again by the first page; if it is no good the person lands on the sign-in page.
  }
  await navigate({ to: "/app" });
});

/** The second step is done: the session is open (the answer carried the operator verdict), so the person goes home. */
const finish = () => navigate({ to: "/app" });
const restart = () => navigate({ to: "/signin" });
</script>

<template>
  <AuthShell :title="t('product.signIn.completing')" :intro="challenge ? undefined : t('product.signIn.completingIntro')">
    <TwoFactorStep v-if="challenge" :challenge="challenge" @done="finish" @restart="restart" />
    <p v-else class="text-muted-foreground text-sm" role="status">{{ t("product.signIn.completingIntro") }}</p>
  </AuthShell>
</template>
