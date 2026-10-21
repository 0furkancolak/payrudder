<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { setActiveAccount } from "@/shared/lib/session";
import { useAccount, useMe } from "./api";
import MembersPanel from "./MembersPanel.vue";

/** The members of the organisation and its invitations, on a page of their own (they used to sit under the settings). */
const { t } = useI18n();
const navigate = useNavigate();
const me = useMe();
const account = useAccount();

/** Left the account: the choice is forgotten (the next account, if any, becomes the first) and the home page shows what is left. */
async function onLeft() {
  setActiveAccount(null);
  await navigate({ to: "/app" });
}
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="px-1">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("shell.items.members") }}</h1>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ t("product.membersPage.intro") }}</p>
    </header>
    <MembersPanel :account="account.data.value" :my-id="me.data.value?.user?.id ?? ''" :loading="account.isPending.value" @left="onLeft" />
  </section>
</template>
