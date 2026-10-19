<script setup lang="ts">
import { Link, useNavigate } from "@tanstack/vue-router";
import { useQueryClient } from "@tanstack/vue-query";
import { useI18n } from "vue-i18n";
import { operatorToken, setOperatorToken } from "@/shared/lib/session";
import LanguageSwitcher from "./LanguageSwitcher.vue";

const { t } = useI18n();
const navigate = useNavigate();
const queries = useQueryClient();

async function signOut() {
  setOperatorToken(null);
  queries.clear();
  await navigate({ to: "/" });
}
</script>

<template>
  <nav>
    <strong>Quotify</strong>
    <template v-if="operatorToken !== null">
      <Link to="/tenants">{{ t("nav.tenants") }}</Link>
      <Link to="/playground">{{ t("nav.playground") }}</Link>
      <Link to="/dead-letters">{{ t("nav.deadLetters") }}</Link>
    </template>
    <span class="spacer">
      <LanguageSwitcher />
      <button v-if="operatorToken !== null" class="link" type="button" @click="signOut">{{ t("nav.signOut") }}</button>
    </span>
  </nav>
</template>
