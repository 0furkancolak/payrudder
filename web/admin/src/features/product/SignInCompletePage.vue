<script setup lang="ts">
import { useQueryClient } from "@tanstack/vue-query";
import { useNavigate } from "@tanstack/vue-router";
import { onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { api, unwrap } from "@/shared/api/client";
import { takeFragmentToken } from "@/shared/lib/fragment";
import { setOperatorFlag, setPersonToken } from "@/shared/lib/session";
import AuthShell from "./AuthShell.vue";

/**
 * Where the gateway sends the browser after "Continue with Google". The session token is in the URL fragment (never sent to a server, so in no log): it is
 * read once, removed from the address bar, kept for this tab, and the person is taken home. A visit without a token goes back to the sign-in page.
 */
const { t } = useI18n();
const navigate = useNavigate();
const queries = useQueryClient();

onMounted(async () => {
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
</script>

<template>
  <AuthShell :title="t('product.signIn.completing')" :intro="t('product.signIn.completingIntro')">
    <p class="text-muted-foreground text-sm" role="status">{{ t("product.signIn.completingIntro") }}</p>
  </AuthShell>
</template>
