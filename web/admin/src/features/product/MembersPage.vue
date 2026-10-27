<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { useQueryClient } from "@tanstack/vue-query";
import { PageHeader } from "@/shared/ui/page-header";
import { leaveAccount, useAccount, useMe } from "./api";
import MembersPanel from "./MembersPanel.vue";

/** The members of the organisation and its invitations, on a page of their own (they used to sit under the settings). */
const { t } = useI18n();
const navigate = useNavigate();
const queries = useQueryClient();
const me = useMe();
const account = useAccount();

/** Left the account: the choice is forgotten (the next account, if any, becomes the first) and the home page shows what is left. */
async function onLeft() {
  leaveAccount(queries);
  await navigate({ to: "/app" });
}
</script>

<template>
  <section class="flex flex-col gap-6">
    <PageHeader flush :title="t('shell.items.members')" :description="t('product.membersPage.intro')" />
    <MembersPanel :account="account.data.value" :my-id="me.data.value?.user?.id ?? ''" :loading="account.isPending.value" @left="onLeft" />
  </section>
</template>
