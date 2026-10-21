<script setup lang="ts">
import { Eye, EyeOff } from "@lucide/vue";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { Input } from "@/shared/ui/input";

/** A password input with a show/hide button. The button is a real button with a label, reachable by keyboard. */
const { t } = useI18n();
defineProps<{ id: string; autocomplete: "current-password" | "new-password"; describedby?: string }>();
const model = defineModel<string>({ required: true });
const shown = ref(false);
</script>

<template>
  <div class="relative">
    <Input :id="id" v-model="model" :type="shown ? 'text' : 'password'" :autocomplete="autocomplete" :aria-describedby="describedby" class="pe-11" required />
    <button
      type="button"
      class="text-muted-foreground hover:text-foreground absolute inset-y-0 end-0 flex w-11 items-center justify-center"
      :aria-label="shown ? t('product.auth.hidePassword') : t('product.auth.showPassword')"
      :aria-pressed="shown"
      @click="shown = !shown"
    >
      <EyeOff v-if="shown" class="size-4" /><Eye v-else class="size-4" />
    </button>
  </div>
</template>
