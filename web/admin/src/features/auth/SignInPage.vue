<script setup lang="ts">
import { useMutation } from "@tanstack/vue-query";
import { useNavigate } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { useErrorText } from "@/shared/lib/errors";
import { prefilledOperatorToken } from "@/shared/lib/prefill";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { signIn } from "./api";

const { t } = useI18n();
const errorText = useErrorText();
const navigate = useNavigate();
const token = ref(prefilledOperatorToken());

const login = useMutation({
  mutationFn: () => signIn(token.value.trim()),
  onSuccess: () => navigate({ to: "/tenants" }),
});
const error = computed(() => (login.error.value ? `${toApiError(login.error.value).code}: ${errorText(login.error.value)}` : null));
</script>

<template>
  <div class="mx-auto w-full max-w-sm">
    <Card class="gap-5 p-6">
      <div>
        <h1 class="text-2xl font-semibold tracking-tight">{{ t("signIn.title") }}</h1>
        <p class="text-muted-foreground mt-1 text-sm">{{ t("signIn.intro") }}</p>
      </div>
      <form class="flex flex-col gap-3" @submit.prevent="login.mutate()">
        <Input v-model="token" :aria-label="t('signIn.tokenLabel')" type="password" autocomplete="off" :placeholder="t('signIn.tokenLabel')" />
        <Button type="submit" size="lg" :disabled="login.isPending.value || token.trim() === ''">{{ t("signIn.submit") }}</Button>
      </form>
      <p v-if="error" class="text-destructive text-sm" role="alert">{{ error }}</p>
    </Card>
  </div>
</template>
