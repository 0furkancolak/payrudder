<script setup lang="ts">
import { useMutation } from "@tanstack/vue-query";
import { useNavigate } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { signIn } from "./api";

const { t } = useI18n();
const navigate = useNavigate();
const token = ref("");

const login = useMutation({
  mutationFn: () => signIn(token.value.trim()),
  onSuccess: () => navigate({ to: "/tenants" }),
});
const error = computed(() => (login.error.value ? `${toApiError(login.error.value).code}: ${toApiError(login.error.value).message}` : null));
</script>

<template>
  <h1>{{ t("signIn.title") }}</h1>
  <p class="muted">{{ t("signIn.intro") }}</p>
  <form class="row" @submit.prevent="login.mutate()">
    <input v-model="token" :aria-label="t('signIn.tokenLabel')" type="password" autocomplete="off" :placeholder="t('signIn.tokenLabel')" />
    <button type="submit" :disabled="login.isPending.value || token.trim() === ''">{{ t("signIn.submit") }}</button>
  </form>
  <p v-if="error" class="error">{{ error }}</p>
</template>
