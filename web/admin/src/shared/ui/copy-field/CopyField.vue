<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { copyText } from "@/shared/lib/clipboard";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";

/**
 * A value shown once for the person to take with them (a payment link, a portal link): a read-only field that selects itself on focus, and a button that
 * copies it. The copy says whether it worked ("Copied") or that the browser refused (select it and copy by hand), instead of failing silently.
 * The default slot is for a note next to the button.
 */
defineProps<{ value: string; label: string; copyLabel: string; testid?: string }>();
const { t } = useI18n();
const state = ref<"idle" | "copied" | "failed">("idle");

async function copy(value: string) {
  state.value = (await copyText(value)) ? "copied" : "failed";
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <Input :model-value="value" readonly class="mono max-w-xl" :aria-label="label" :data-testid="testid" @focus="($event.target as HTMLInputElement).select()" />
    <Button type="button" variant="outline" @click="copy(value)">{{ copyLabel }}</Button>
    <slot />
    <span v-if="state === 'copied'" class="text-muted-foreground text-xs" role="status">{{ t("common.copied") }}</span>
    <span v-else-if="state === 'failed'" class="text-warning text-xs" role="status">{{ t("common.copyFailed") }}</span>
  </div>
</template>
