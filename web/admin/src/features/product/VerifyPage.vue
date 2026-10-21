<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { takeFragmentToken } from "@/shared/lib/fragment";
import { isPerson } from "@/shared/lib/session";
import { Button } from "@/shared/ui/button";
import { useVerifyEmail } from "./api";
import AuthShell from "./AuthShell.vue";

/** The page the verification mail opens: /verify#token=… . It spends the token once on load and says what happened. */
const { t } = useI18n();
const verify = useVerifyEmail();
const missing = ref(false);

onMounted(() => {
  const token = takeFragmentToken();
  if (token === "") missing.value = true;
  else verify.mutate(token);
});
</script>

<template>
  <AuthShell :title="t('product.verifyPage.title')" :intro="t('product.verifyPage.intro')">
    <p v-if="verify.isPending.value" class="text-muted-foreground text-sm" role="status">{{ t("product.verifyPage.working") }}</p>
    <div v-else-if="verify.isSuccess.value" class="bg-success-soft text-success flex flex-col gap-1 rounded-2xl px-4 py-3 text-sm" role="status">
      <p class="font-medium">{{ t("product.verifyPage.doneTitle") }}</p>
      <p>{{ t("product.verifyPage.doneBody") }}</p>
    </div>
    <div v-else-if="verify.isError.value || missing" class="bg-destructive-soft text-destructive flex flex-col gap-1 rounded-2xl px-4 py-3 text-sm" role="alert">
      <p class="font-medium">{{ t("product.verifyPage.failTitle") }}</p>
      <p>{{ t("product.verifyPage.failBody") }}</p>
    </div>
    <Button v-if="!verify.isPending.value" as-child size="lg">
      <Link :to="isPerson ? '/app' : '/signin'">{{ isPerson ? t("product.verifyPage.toApp") : t("product.signIn.cta") }}</Link>
    </Button>
  </AuthShell>
</template>
