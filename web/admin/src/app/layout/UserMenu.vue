<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { Check, LogOut, Settings } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useMe, useSignOut, useSwitchAccount } from "@/features/product/api";
import { ownMemberships } from "@/features/product/lib";
import { activeAccount } from "@/shared/lib/session";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/shared/ui/dropdown-menu";

const { t } = useI18n();
const navigate = useNavigate();
const me = useMe();
const signOut = useSignOut();
const switchTo = useSwitchAccount();

// The organisations the person works in (a seller's partner membership is not one); the current one is ticked. Choosing another moves the whole console to it.
const orgs = computed(() => ownMemberships(me.data.value?.memberships));
const currentOrg = computed(() => (orgs.value.find((m) => m.account_id === activeAccount.value) ?? orgs.value[0])?.account_id ?? "");

const name = computed(() => me.data.value?.user?.display_name || me.data.value?.user?.email || "");
const initials = computed(() => {
  const words = (me.data.value?.user?.display_name || me.data.value?.user?.email?.split("@")[0] || "?").split(/[\s._-]+/).filter(Boolean);
  return (words[0]?.[0] ?? "?").toUpperCase() + (words.length > 1 ? (words[1]?.[0] ?? "").toUpperCase() : "");
});

async function leave() {
  await signOut.mutateAsync();
  await navigate({ to: "/signin" });
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <button
        type="button"
        class="bg-brand text-rail shadow-card inline-flex size-10 items-center justify-center rounded-xl text-sm font-semibold transition-transform hover:scale-105"
        :aria-label="t('product.menu.open')"
      >
        {{ initials }}
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="min-w-56">
      <DropdownMenuLabel class="font-normal">
        <span class="block truncate text-sm font-medium">{{ name }}</span>
        <span v-if="me.data.value?.user?.display_name" class="text-muted-foreground block truncate text-xs">{{ me.data.value?.user?.email }}</span>
      </DropdownMenuLabel>
      <template v-if="orgs.length > 0">
        <DropdownMenuSeparator />
        <DropdownMenuLabel class="text-muted-foreground text-xs">{{ t("shell.org") }}</DropdownMenuLabel>
        <DropdownMenuItem v-for="m in orgs" :key="m.account_id" class="rounded-xl" :data-testid="`switch-org-${m.account_id}`" @select="switchTo(m.account_id ?? '')">
          <span class="flex-1 truncate">{{ m.account_name }}</span>
          <Check v-if="m.account_id === currentOrg" class="size-4" aria-hidden="true" />
        </DropdownMenuItem>
      </template>
      <DropdownMenuSeparator />
      <DropdownMenuItem class="rounded-xl" @select="navigate({ to: '/app/settings' })"><Settings class="size-4" />{{ t("product.nav.settings") }}</DropdownMenuItem>
      <DropdownMenuItem class="rounded-xl" @select="leave"><LogOut class="size-4" />{{ t("nav.signOut") }}</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
