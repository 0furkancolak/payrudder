<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import GoogleButton from "./GoogleButton.vue";

/** How a person proves who they are, on the sign-in and the sign-up page alike: Google (only when the deployment has set it up), then a password or a mailed code. */
defineProps<{ google?: boolean }>();
const method = defineModel<string>({ required: true });
const { t } = useI18n();
</script>

<template>
  <template v-if="google">
    <GoogleButton />
    <div class="text-muted-foreground flex items-center gap-3 text-xs" aria-hidden="true">
      <span class="bg-border h-px flex-1" />{{ t("product.signIn.or") }}<span class="bg-border h-px flex-1" />
    </div>
  </template>
  <Tabs v-model="method">
    <TabsList class="w-full [&>*]:flex-1" :aria-label="t('product.signIn.methods')">
      <TabsTrigger value="password">{{ t("product.signIn.tabPassword") }}</TabsTrigger>
      <TabsTrigger value="code">{{ t("product.signIn.tabCode") }}</TabsTrigger>
    </TabsList>
  </Tabs>
</template>
