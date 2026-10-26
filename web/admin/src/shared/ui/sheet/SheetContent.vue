<script setup lang="ts">
import type { DialogContentEmits, DialogContentProps } from "reka-ui";
import type { HTMLAttributes } from "vue";
import { X } from "@lucide/vue";
import { reactiveOmit } from "@vueuse/core";
import { DialogClose, DialogContent, DialogOverlay, DialogPortal, useForwardPropsEmits } from "reka-ui";
import { useI18n } from "vue-i18n";
import { cn } from "@/shared/lib/utils";

/**
 * A panel that slides in from the side (the end side by default) and holds a form, so the page behind it stays in view. It is the one place the
 * console asks for input: a long form scrolls inside the body while the header and the footer stay put.
 */
defineOptions({ inheritAttrs: false });
const props = withDefaults(defineProps<DialogContentProps & { class?: HTMLAttributes["class"]; side?: "end" | "start"; showCloseButton?: boolean }>(), { side: "end", showCloseButton: true });
const emits = defineEmits<DialogContentEmits>();
const delegated = reactiveOmit(props, "class", "side", "showCloseButton");
const forwarded = useForwardPropsEmits(delegated, emits);
const { t } = useI18n();
</script>

<template>
  <DialogPortal>
    <DialogOverlay data-slot="sheet-overlay" class="data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed inset-0 z-50 bg-black/50 backdrop-blur-[2px]" />
    <DialogContent
      data-slot="sheet-content"
      v-bind="{ ...$attrs, ...forwarded }"
      :class="
        cn(
          'bg-card shadow-pop data-[state=open]:animate-in data-[state=closed]:animate-out fixed inset-y-0 z-50 flex w-full flex-col gap-0 transition ease-in-out data-[state=closed]:duration-200 data-[state=open]:duration-300 sm:inset-y-3 sm:max-w-md sm:rounded-3xl sm:border',
          side === 'end'
            ? 'data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right end-0 sm:end-3 rtl:data-[state=closed]:slide-out-to-left rtl:data-[state=open]:slide-in-from-left'
            : 'data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left start-0 sm:start-3 rtl:data-[state=closed]:slide-out-to-right rtl:data-[state=open]:slide-in-from-right',
          props.class,
        )
      "
    >
      <slot />
      <DialogClose v-if="showCloseButton" data-slot="sheet-close" class="text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-ring/50 absolute end-4 top-4 rounded-full p-1.5 transition-colors focus-visible:ring-[3px] focus-visible:outline-none [&_svg]:size-4">
        <X aria-hidden="true" />
        <span class="sr-only">{{ t("common.close") }}</span>
      </DialogClose>
    </DialogContent>
  </DialogPortal>
</template>
