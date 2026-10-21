<script setup lang="ts">
import { X } from "@lucide/vue";
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from "reka-ui";
import { useI18n } from "vue-i18n";
import { isOperator } from "@/shared/lib/session";
import { LogOut } from "@lucide/vue";
import SidebarNav from "./SidebarNav.vue";
import type { NavItem, NavSection } from "./nav";

/**
 * The navigation on a phone or a tablet: a drawer from the start edge (the left in English, the right in Arabic). It is a modal dialog, so focus moves into
 * it when it opens, stays inside while it is open, Escape closes it and focus returns to the menu button; choosing a link closes it.
 */
defineProps<{ sections: NavSection[]; current: NavItem | undefined; brand: string }>();
const open = defineModel<boolean>("open", { required: true });
const emit = defineEmits<{ signOut: [] }>();
const { t } = useI18n();
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed inset-0 z-50 bg-black/60 lg:hidden" />
      <DialogContent
        data-testid="drawer"
        class="bg-card shadow-pop data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left rtl:data-[state=closed]:slide-out-to-right rtl:data-[state=open]:slide-in-from-right fixed inset-y-0 start-0 z-50 flex w-[min(20rem,calc(100vw-3rem))] flex-col overflow-hidden rounded-e-3xl p-4 duration-200 outline-none lg:hidden"
      >
        <div class="mb-4 flex items-center justify-between gap-3">
          <div class="flex items-center gap-2.5 px-1">
            <span class="bg-brand text-rail flex size-10 items-center justify-center rounded-2xl text-lg font-bold" aria-hidden="true">{{ brand.slice(0, 1) }}</span>
            <DialogTitle class="text-lg font-semibold tracking-tight">{{ brand }}</DialogTitle>
          </div>
          <button type="button" class="text-muted-foreground hover:bg-muted hover:text-foreground flex size-11 items-center justify-center rounded-2xl" :aria-label="t('shell.closeMenu')" @click="open = false">
            <X class="size-5" aria-hidden="true" />
          </button>
        </div>
        <DialogDescription class="sr-only">{{ t("shell.navLabel") }}</DialogDescription>
        <div class="-mx-1 min-h-0 flex-1 overflow-y-auto px-1">
          <SidebarNav :sections="sections" :current="current" :label="t('shell.navLabel')" @navigate="open = false" />
        </div>
        <button
          v-if="isOperator"
          type="button"
          class="text-muted-foreground hover:bg-muted hover:text-foreground mt-3 flex min-h-11 items-center gap-3 rounded-2xl border-t px-3.5 pt-3 text-sm font-medium"
          @click="emit('signOut')"
        >
          <LogOut class="size-[1.15rem]" aria-hidden="true" />{{ t("nav.signOut") }}
        </button>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
