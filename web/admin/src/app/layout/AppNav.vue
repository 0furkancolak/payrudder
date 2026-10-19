<script setup lang="ts">
import { Link, useNavigate } from "@tanstack/vue-router";
import { useQueryClient } from "@tanstack/vue-query";
import { FlaskConical, Inbox, LogOut, Users } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { operatorToken, setOperatorToken } from "@/shared/lib/session";
import LanguageSwitcher from "./LanguageSwitcher.vue";

const { t } = useI18n();
const navigate = useNavigate();
const queries = useQueryClient();

const item = "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground";
const itemActive = "bg-accent !text-foreground font-medium";

async function signOut() {
  setOperatorToken(null);
  queries.clear();
  await navigate({ to: "/" });
}
</script>

<template>
  <aside
    v-if="operatorToken !== null"
    class="bg-card flex shrink-0 gap-3 border-b px-4 py-3 md:sticky md:top-0 md:h-screen md:w-56 md:flex-col md:border-e md:border-b-0 md:px-3 md:py-5"
  >
    <strong class="px-3 text-lg font-semibold tracking-tight md:pb-4">Quotify</strong>
    <nav class="flex flex-1 gap-1 overflow-x-auto md:flex-col">
      <Link to="/tenants" :class="item" :active-props="{ class: itemActive }"><Users class="size-4" />{{ t("nav.tenants") }}</Link>
      <Link to="/playground" :class="item" :active-props="{ class: itemActive }"><FlaskConical class="size-4" />{{ t("nav.playground") }}</Link>
      <Link to="/dead-letters" :class="item" :active-props="{ class: itemActive }"><Inbox class="size-4" />{{ t("nav.deadLetters") }}</Link>
    </nav>
    <div class="ms-auto flex items-center gap-1 md:ms-0 md:flex-col md:items-stretch">
      <LanguageSwitcher />
      <button type="button" :class="item" @click="signOut"><LogOut class="size-4" />{{ t("nav.signOut") }}</button>
    </div>
  </aside>
  <header v-else class="fixed inset-x-0 top-0 flex items-center justify-between px-4 py-3 md:px-10 md:py-5">
    <strong class="text-lg font-semibold tracking-tight">Quotify</strong>
    <LanguageSwitcher />
  </header>
</template>
