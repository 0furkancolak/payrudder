<script setup lang="ts">
import { Link, useNavigate } from "@tanstack/vue-router";
import { useQueryClient } from "@tanstack/vue-query";
import { LogOut } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { operatorToken, setOperatorToken } from "@/shared/lib/session";
import { Button } from "@/shared/ui/button";
import LanguageSwitcher from "./LanguageSwitcher.vue";

const { t } = useI18n();
const navigate = useNavigate();
const queries = useQueryClient();

const pill = "rounded-full px-4 py-2 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground";
const pillActive = "bg-lime !text-lime-foreground";

async function signOut() {
  setOperatorToken(null);
  queries.clear();
  await navigate({ to: "/" });
}
</script>

<template>
  <nav class="flex flex-wrap items-center gap-x-4 gap-y-3">
    <div class="flex items-center gap-3">
      <span class="bg-primary text-lime grid size-10 place-items-center rounded-full text-lg font-semibold" aria-hidden="true">Q</span>
      <strong class="text-xl font-semibold tracking-tight">Quotify</strong>
    </div>
    <div v-if="operatorToken !== null" class="order-last flex w-full justify-center gap-1 overflow-x-auto md:order-none md:w-auto md:flex-1">
      <Link to="/tenants" :class="pill" :active-props="{ class: pillActive }">{{ t("nav.tenants") }}</Link>
      <Link to="/playground" :class="pill" :active-props="{ class: pillActive }">{{ t("nav.playground") }}</Link>
      <Link to="/dead-letters" :class="pill" :active-props="{ class: pillActive }">{{ t("nav.deadLetters") }}</Link>
    </div>
    <div class="ms-auto flex items-center gap-2">
      <LanguageSwitcher />
      <Button v-if="operatorToken !== null" variant="outline" size="sm" type="button" @click="signOut"><LogOut />{{ t("nav.signOut") }}</Button>
    </div>
  </nav>
</template>
