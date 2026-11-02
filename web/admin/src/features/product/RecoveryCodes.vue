<script setup lang="ts">
import { Copy, Download } from "@lucide/vue";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { copyText } from "@/shared/lib/clipboard";
import { todayYmd } from "@/shared/lib/dates";
import { saveBlob } from "@/shared/lib/download";
import { Button } from "@/shared/ui/button";
import { Checkbox } from "@/shared/ui/checkbox";
import { RECOVERY_FILE_NAME, recoveryFileText } from "./twoFactor";

/**
 * The ten recovery codes, shown once: a way to download them as a text file (made here, with the address and the day on its first line), a way to copy them,
 * and a box the person must tick to say they have kept them before "Done" works. The codes are only in this component's props: nothing stores them.
 */
const props = defineProps<{ codes: readonly string[]; email: string }>();
const emit = defineEmits<{ done: [] }>();
const { t } = useI18n();
const saved = ref(false);
const copied = ref<"idle" | "copied" | "failed">("idle");

function download(): void {
  const text = recoveryFileText({ title: t("product.twoFactor.file.title", { email: props.email, date: todayYmd() }), note: t("product.twoFactor.file.note"), codes: props.codes });
  saveBlob(RECOVERY_FILE_NAME, new Blob([text], { type: "text/plain;charset=utf-8" }));
}

async function copy(): Promise<void> {
  copied.value = (await copyText(props.codes.join("\n"))) ? "copied" : "failed";
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="recovery-codes">
    <p class="text-muted-foreground text-sm">{{ t("product.twoFactor.codes.body") }}</p>
    <ul class="bg-muted mono grid grid-cols-2 gap-x-6 gap-y-1.5 rounded-2xl p-4 text-sm select-all" dir="ltr" :aria-label="t('product.twoFactor.codes.list')" data-testid="recovery-code-list">
      <li v-for="c in codes" :key="c">{{ c }}</li>
    </ul>
    <div class="flex flex-wrap items-center gap-2">
      <Button type="button" variant="outline" data-testid="codes-download" @click="download"><Download />{{ t("product.twoFactor.codes.download") }}</Button>
      <Button type="button" variant="outline" data-testid="codes-copy" @click="copy"><Copy />{{ t("product.twoFactor.codes.copy") }}</Button>
      <span v-if="copied === 'copied'" class="text-muted-foreground text-xs" role="status">{{ t("common.copied") }}</span>
      <span v-else-if="copied === 'failed'" class="text-warning text-xs" role="status">{{ t("common.copyFailed") }}</span>
    </div>
    <label class="flex items-start gap-2 text-sm">
      <Checkbox class="mt-0.5" :model-value="saved" data-testid="codes-saved" @update:model-value="(v: boolean | 'indeterminate') => (saved = v === true)" />
      <span>{{ t("product.twoFactor.codes.saved") }}</span>
    </label>
    <Button type="button" :disabled="!saved" data-testid="codes-done" @click="emit('done')">{{ t("product.twoFactor.codes.done") }}</Button>
  </div>
</template>
