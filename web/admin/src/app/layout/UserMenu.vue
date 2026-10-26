<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { Check, LogOut, Plus, Settings } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useCreateAccount, useMe, useSignOut, useSwitchAccount } from "@/features/product/api";
import { ownMemberships } from "@/features/product/lib";
import { activeAccount } from "@/shared/lib/session";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
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

// Starting another organisation: the person owns it, the console moves to it and lands on its home.
const creating = ref(false);
const orgName = ref("");
const createOrg = useCreateAccount();

function startCreating() {
  orgName.value = "";
  createOrg.reset();
  creating.value = true;
}

async function submitOrg() {
  const trimmed = orgName.value.trim();
  if (!trimmed) return;
  try {
    await createOrg.mutateAsync(trimmed);
  } catch {
    return; // shown under the field
  }
  creating.value = false;
  await navigate({ to: "/app" });
}

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
        data-testid="user-menu"
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
      <DropdownMenuItem class="rounded-xl" data-testid="new-org" @select="startCreating"><Plus class="size-4" />{{ t("product.menu.newOrg") }}</DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuItem class="rounded-xl" @select="navigate({ to: '/app/settings' })"><Settings class="size-4" />{{ t("product.nav.settings") }}</DropdownMenuItem>
      <DropdownMenuItem class="rounded-xl" @select="leave"><LogOut class="size-4" />{{ t("nav.signOut") }}</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>

  <Sheet v-model:open="creating" name="new-organisation">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("product.menu.newOrgTitle") }}</SheetTitle>
        <SheetDescription>{{ t("product.menu.newOrgBody") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-org" class="grid gap-3" @submit.prevent="submitOrg">
          <Input v-model="orgName" :aria-label="t('product.menu.orgName')" :placeholder="t('product.menu.orgName')" maxlength="200" required autofocus />
          <ErrorText :error="createOrg.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="creating = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-org" :disabled="createOrg.isPending.value || !orgName.trim()">{{ t("product.menu.orgCreate") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
