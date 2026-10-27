<script setup lang="ts">
import { Link, useNavigate } from "@tanstack/vue-router";
import { LogOut } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { firstError } from "@/shared/lib/errors";
import { applyTheme, theme, type ThemeChoice } from "@/shared/lib/theme";
import { useSaasEnabled } from "@/features/saas/api";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { SegmentedControl } from "@/shared/ui/segmented";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useAccount, useMe, useRenameAccount, useSignOut } from "./api";
import AccountSwitcher from "./AccountSwitcher.vue";

const { t } = useI18n();
const fmt = useFormat();
const navigate = useNavigate();
const me = useMe();
const saas = useSaasEnabled();
const account = useAccount();
const signOut = useSignOut();

const rename = useRenameAccount();
const accountName = ref("");
watch(() => account.data.value?.name, (n) => (accountName.value = n ?? ""), { immediate: true });
const isOwner = computed(() => account.data.value?.role === "owner");
const nameChanged = computed(() => accountName.value.trim() !== "" && accountName.value.trim() !== account.data.value?.name);

const when = (v?: string | null) => fmt.date(v, "dateTime");
const themes = computed(() => [
  { value: "system", label: t("product.settings.themeSystem") },
  { value: "light", label: t("product.settings.themeLight") },
  { value: "dark", label: t("product.settings.themeDark") },
]);

async function leave() {
  await signOut.mutateAsync();
  await navigate({ to: "/signin", search: { app: undefined } as never });
}
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="px-1">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("product.settings.title") }}</h1>
      <p class="text-muted-foreground mt-1 text-sm">{{ t("product.settings.intro") }}</p>
    </header>
    <ErrorText :error="firstError(account, me)" class="bg-destructive-soft rounded-2xl px-4 py-3" />

    <Link v-if="saas" to="/app/settings/plan" class="surface hover:border-ring flex flex-wrap items-center justify-between gap-2 p-5" data-testid="plan-link">
      <span class="font-semibold">{{ t("saas.title") }}</span>
      <span class="text-muted-foreground text-sm">{{ t("saas.linkHint") }}</span>
    </Link>

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
        <AccountSwitcher />
        <form v-if="isOwner" class="border-border/70 flex flex-wrap items-end gap-3 border-t pt-4" @submit.prevent="rename.mutate(accountName.trim())">
          <div class="flex min-w-48 flex-1 flex-col gap-1.5">
            <Label for="acct-name">{{ t("product.account.rename") }}</Label>
            <Input id="acct-name" v-model="accountName" maxlength="200" required />
          </div>
          <Button type="submit" variant="outline" :disabled="rename.isPending.value || !nameChanged">{{ t("product.account.save") }}</Button>
          <ErrorText :error="rename.error.value" class="w-full" />
        </form>
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
</section>
</template>
