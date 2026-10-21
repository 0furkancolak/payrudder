<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { LogOut } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { applyTheme, theme, type ThemeChoice } from "@/shared/lib/theme";
import { Button } from "@/shared/ui/button";
import { SegmentedControl } from "@/shared/ui/segmented";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useAccount, useMe, useSignOut } from "./api";

const { t, locale } = useI18n();
const errorText = useErrorText();
const navigate = useNavigate();
const me = useMe();
const account = useAccount();
const signOut = useSignOut();

const members = computed(() => account.data.value?.members ?? []);
const when = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium", timeStyle: "short" }).format(new Date(v)) : "");
const themes = computed(() => [
  { value: "system", label: t("product.settings.themeSystem") },
  { value: "light", label: t("product.settings.themeLight") },
  { value: "dark", label: t("product.settings.themeDark") },
]);

async function leave() {
  await signOut.mutateAsync();
  await navigate({ to: "/signin" });
}
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="px-1">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("product.settings.title") }}</h1>
      <p class="text-muted-foreground mt-1 text-sm">{{ t("product.settings.intro") }}</p>
    </header>
    <p v-if="account.error.value || me.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(account.error.value ?? me.error.value) }}</p>

    <div class="grid gap-5 lg:grid-cols-2">
      <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.settings.account')">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.settings.account") }}</h2>
        <Skeleton v-if="account.isPending.value" class="h-16 rounded-2xl" />
        <dl v-else class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
          <dt class="text-muted-foreground">{{ t("product.settings.organisation") }}</dt>
          <dd class="font-medium" data-testid="account-name">{{ account.data.value?.name }}</dd>
          <dt class="text-muted-foreground">{{ t("product.settings.yourRole") }}</dt>
          <dd><StatusPill tone="info">{{ t(`product.role.${account.data.value?.role}`) }}</StatusPill></dd>
          <dt class="text-muted-foreground">{{ t("product.settings.created") }}</dt>
          <dd>{{ when(account.data.value?.create_time) }}</dd>
        </dl>
      </section>

      <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.settings.you')">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.settings.you") }}</h2>
        <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
          <dt class="text-muted-foreground">{{ t("product.auth.name") }}</dt>
          <dd class="font-medium">{{ me.data.value?.user?.display_name || "—" }}</dd>
          <dt class="text-muted-foreground">{{ t("product.auth.email") }}</dt>
          <dd class="font-medium break-all">{{ me.data.value?.user?.email }}</dd>
          <dt class="text-muted-foreground">{{ t("product.settings.sessionEnds") }}</dt>
          <dd>{{ when(me.data.value?.expire_time) }}</dd>
        </dl>
        <div class="flex flex-wrap items-center justify-between gap-3 border-t pt-4">
          <div class="flex items-center gap-3">
            <span class="text-muted-foreground text-sm">{{ t("product.settings.appearance") }}</span>
            <SegmentedControl :model-value="theme" :options="themes" :label="t('product.settings.appearance')" @update:model-value="(v) => applyTheme(v as ThemeChoice)" />
          </div>
          <Button variant="outline" :disabled="signOut.isPending.value" @click="leave"><LogOut />{{ t("nav.signOut") }}</Button>
        </div>
      </section>
    </div>

    <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.settings.members')">
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.settings.members") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("product.settings.membersNote") }}</p>
      </div>
      <Skeleton v-if="account.isPending.value" class="h-20 rounded-2xl" />
      <ul v-else class="divide-border/70 divide-y">
        <li v-for="m in members" :key="m.user_id" class="flex items-center gap-4 py-3">
          <span class="bg-brand-soft text-brand-strong flex size-10 shrink-0 items-center justify-center rounded-xl text-sm font-semibold" aria-hidden="true">{{ (m.display_name || m.email || "?").slice(0, 1).toUpperCase() }}</span>
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-medium">{{ m.display_name || m.email }}</p>
            <p v-if="m.display_name" class="text-muted-foreground truncate text-xs">{{ m.email }}</p>
          </div>
          <StatusPill :tone="m.role === 'owner' ? 'info' : 'neutral'">{{ t(`product.role.${m.role}`) }}</StatusPill>
        </li>
      </ul>
    </section>
  </section>
</template>
