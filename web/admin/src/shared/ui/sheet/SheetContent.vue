<script setup lang="ts">
import type { DialogContentEmits, DialogContentProps } from "reka-ui";
import type { HTMLAttributes } from "vue";
import { X } from "@lucide/vue";
import { reactiveOmit } from "@vueuse/core";
import { DialogClose, DialogContent, DialogPortal, Presence, useForwardPropsEmits } from "reka-ui";
import { computed, inject } from "vue";
import { useI18n } from "vue-i18n";
import { closeFront, depthOf, openTabs } from "@/shared/lib/sheets";
import { cn } from "@/shared/lib/utils";
import SheetStrip from "./SheetStrip.vue";
import { SHEET_CONTEXT } from "./context";

/**
 * A panel that slides in from the side (the end side by default) and holds a form, so the page behind it stays in view. It is the one place the
 * console asks for input: a long form scrolls inside the body while the header and the footer stay put.
 *
 * Open sheets stack: the one in front is on top, each one behind it shows a sliver of itself, and the strip at the top of the front one lists them all.
 * The dialog is not modal (a sheet behind the front one must stay usable once it is raised), so the overlay is drawn here, and the dismissals a modal
 * dialog does by itself are done here too: the overlay and Escape close the sheet in front, never one behind it.
 */
defineOptions({ inheritAttrs: false });
const props = withDefaults(defineProps<DialogContentProps & { class?: HTMLAttributes["class"]; side?: "end" | "start"; showCloseButton?: boolean }>(), { side: "end", showCloseButton: true });
const emits = defineEmits<DialogContentEmits>();
const delegated = reactiveOmit(props, "class", "side", "showCloseButton");
const forwarded = useForwardPropsEmits(delegated, emits);
const { t } = useI18n();
const sheet = inject(SHEET_CONTEXT, null);
const depth = computed(() => (sheet ? Math.max(depthOf(sheet.uid), 0) : 0));
const behind = computed(() => openTabs.value.length - 1 - depth.value);
// Under the dropdowns and popovers (z-50) so that those open over a sheet in any place of the stack, over the header (z-30).
/**
 * A sheet that opens on a form puts the focus on its first field, not on the "App" select that heads every form (that one is changed rarely, and
 * focusing it made typing start in the wrong place). A sheet without a form keeps the default (its first focusable thing); one behind another keeps the focus where it is.
 */
function focusFirstField(e: Event) {
  if (behind.value > 0) return e.preventDefault();
  const content = e.target instanceof HTMLElement ? e.target : null;
  const app = content?.querySelector('[data-testid="app-field"]');
  const fields = [...(content?.querySelectorAll<HTMLElement>('form input:not([type="hidden"]):not([type="checkbox"]):not([disabled]), form textarea:not([disabled]), form select:not([disabled])') ?? [])];
  const target = fields.find((el) => !app?.contains(el)) ?? fields[0];
  if (target) {
    e.preventDefault();
    target.focus();
  }
}
const z = computed(() => Math.min(31 + depth.value * 3, 48));
</script>

<template>
  <DialogPortal>
    <Presence :present="sheet?.open.value ?? true">
      <div
        data-slot="sheet-overlay"
        :data-state="sheet?.open.value === false ? 'closed' : 'open'"
        :class="cn('data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed inset-0', depth === 0 ? 'bg-black/50 backdrop-blur-[2px]' : 'bg-black/10')"
        :style="{ zIndex: z }"
        @click="closeFront()"
      />
    </Presence>
    <DialogContent
      data-slot="sheet-content"
      v-bind="{ ...$attrs, ...forwarded }"
      :style="{ zIndex: z + 1, '--sheet-behind': behind }"
      :class="
        cn(
          'sheet-stacked bg-card shadow-pop data-[state=open]:animate-in data-[state=closed]:animate-out fixed inset-y-0 flex w-full flex-col gap-0 transition ease-in-out data-[state=closed]:duration-200 data-[state=open]:duration-300 sm:inset-y-3 sm:max-w-md sm:rounded-3xl sm:border overflow-hidden overscroll-contain',
          side === 'end'
            ? 'data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right end-0 sm:end-3 rtl:data-[state=closed]:slide-out-to-left rtl:data-[state=open]:slide-in-from-left'
            : 'data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left start-0 sm:start-3 rtl:data-[state=closed]:slide-out-to-right rtl:data-[state=open]:slide-in-from-right',
          props.class,
        )
      "
      @escape-key-down.prevent="closeFront()"
      @interact-outside.prevent
      @open-auto-focus="focusFirstField"
    >
      <SheetStrip v-if="depth === openTabs.length - 1" />
      <slot />
      <DialogClose v-if="showCloseButton" data-slot="sheet-close" class="text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-ring/50 absolute end-4 top-4 rounded-full p-1.5 transition-colors focus-visible:ring-[3px] focus-visible:outline-none [&_svg]:size-4">
        <X aria-hidden="true" />
        <span class="sr-only">{{ t("common.close") }}</span>
      </DialogClose>
    </DialogContent>
  </DialogPortal>
</template>
