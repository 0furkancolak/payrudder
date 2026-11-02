<script setup lang="ts">
import { Paperclip, X } from "@lucide/vue";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { ATTACH_TYPES, MAX_ATTACH_FILES } from "./api";
import { Button } from "@/shared/ui/button";

/**
 * The files chosen for the message being written (png, jpeg, pdf or text; the help desk's limits are in api.ts). They are sent after the message, one by one,
 * so this only keeps the list and lets the person take one out. `files` is the page's own list.
 */
const files = defineModel<File[]>({ required: true });
const { t } = useI18n();
const input = ref<HTMLInputElement>();

function chosen(e: Event) {
  const el = e.target as HTMLInputElement;
  files.value = [...files.value, ...Array.from(el.files ?? [])].slice(0, MAX_ATTACH_FILES + 5);
  el.value = "";
}
</script>

<template>
  <div class="grid gap-1" data-testid="attach-picker">
    <div class="flex flex-wrap items-center gap-2">
      <input ref="input" type="file" multiple class="sr-only" tabindex="-1" :accept="ATTACH_TYPES.join(',')" data-testid="attach-input" @change="chosen" />
      <Button type="button" variant="outline" size="sm" data-testid="attach-add" @click="input?.click()"><Paperclip class="size-4" aria-hidden="true" />{{ t("support.attach.add") }}</Button>
      <span class="text-muted-foreground text-xs">{{ t("support.attach.hint") }}</span>
    </div>
    <ul v-if="files.length > 0" class="flex flex-wrap gap-2" data-testid="attach-chosen">
      <li v-for="(f, i) in files" :key="`${f.name}-${i}`" class="bg-muted flex items-center gap-1 rounded-full py-1 ps-3 pe-1 text-xs">
        <span class="max-w-48 truncate">{{ f.name }}</span>
        <button type="button" class="hover:bg-background rounded-full p-1" :aria-label="t('support.attach.remove', { name: f.name })" @click="files = files.filter((_, j) => j !== i)">
          <X class="size-3" aria-hidden="true" />
        </button>
      </li>
    </ul>
  </div>
</template>
