<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import type { ApiLocale } from "@/shared/lib/locale";
import { FormField } from "@/shared/ui/form-field";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { StatusPill } from "@/shared/ui/status-pill";
import { LANGUAGE_NAMES, LANGUAGES, diffLines, diffStats, languageNames, versionsOf, type Audience, type ContractVersion, type Kind } from "./lib";

/**
 * Every version of one agreement, newest first, and a line diff between any two of them in one language. A version is never edited, so this is the whole
 * record of what the seller's customers were shown and when it changed.
 */
const props = defineProps<{ series: { kind: Kind; audience: Audience } | null; versions: ContractVersion[] }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const fmt = useFormat();

const list = computed(() => (props.series ? versionsOf(props.versions, props.series.kind, props.series.audience) : []));
const from = ref(0);
const to = ref(0);
const language = ref<ApiLocale>("en");
// A sheet that opens compares the newest version with the one before it.
watch(
  () => [open.value, list.value.length] as const,
  ([isOpen]) => {
    if (!isOpen) return;
    to.value = list.value[0]?.number ?? 0;
    from.value = list.value[1]?.number ?? 0;
  },
  { immediate: true },
);
const pick = (n: number) => list.value.find((v) => v.number === n);
const languages = computed(() => LANGUAGES.filter((l) => (pick(from.value)?.bodies[l] ?? "") !== "" || (pick(to.value)?.bodies[l] ?? "") !== ""));
watch(languages, (ls) => {
  if (!ls.includes(language.value) && ls[0]) language.value = ls[0];
}, { immediate: true });
const lines = computed(() => diffLines(pick(from.value)?.bodies[language.value] ?? "", pick(to.value)?.bodies[language.value] ?? ""));
const stats = computed(() => diffStats(lines.value));
const LINE = {
  same: "text-muted-foreground",
  add: "bg-success-soft text-success",
  remove: "bg-destructive-soft text-destructive",
} as const;
const MARK = { same: " ", add: "+", remove: "−" } as const;
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="sm:max-w-2xl" data-testid="history-sheet">
      <SheetHeader>
        <SheetTitle>{{ series ? t("contracts.history.title", { kind: t(`contracts.kinds.${series.kind}`), audience: t(`contracts.audiences.${series.audience}`) }) : "" }}</SheetTitle>
        <SheetDescription>{{ t("contracts.history.description") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <ol class="divide-border/70 m-0 flex list-none flex-col divide-y p-0 text-sm" :aria-label="t('contracts.history.versions')">
          <li v-for="v in list" :key="v.id" class="flex flex-col gap-1 py-3" :data-testid="`history-version-${v.number}`">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-semibold">{{ t("contracts.history.version", { n: v.number }) }}</span>
              <StatusPill v-if="v.current" tone="success">{{ t("contracts.history.current") }}</StatusPill>
              <StatusPill :tone="v.material ? 'info' : 'neutral'">{{ v.material ? t("contracts.agreements.material") : t("contracts.agreements.minor") }}</StatusPill>
              <StatusPill v-if="v.has_template_notice" tone="warning">{{ t("contracts.agreements.templateBadge") }}</StatusPill>
            </div>
            <span class="text-muted-foreground text-xs">{{ fmt.date(v.create_time, "dateTime") }}<template v-if="v.author"> · {{ t("contracts.agreements.by", { author: v.author }) }}</template></span>
            <span class="text-muted-foreground text-xs">{{ languageNames(v) }}</span>
            <span v-if="v.note" class="break-words" dir="auto">{{ v.note }}</span>
          </li>
        </ol>

        <section v-if="list.length > 1" class="grid gap-3 border-t pt-4" :aria-label="t('contracts.history.compare')" data-testid="history-diff">
          <h3 class="m-0 text-sm font-semibold">{{ t("contracts.history.compare") }}</h3>
          <div class="grid gap-3 sm:grid-cols-3">
            <FormField :label="t('contracts.history.from')" for="history-from">
              <NativeSelect id="history-from" v-model.number="from" class="w-full" data-testid="history-from">
                <option v-for="v in list" :key="v.id" :value="v.number">{{ t("contracts.history.version", { n: v.number }) }}</option>
              </NativeSelect>
            </FormField>
            <FormField :label="t('contracts.history.to')" for="history-to">
              <NativeSelect id="history-to" v-model.number="to" class="w-full" data-testid="history-to">
                <option v-for="v in list" :key="v.id" :value="v.number">{{ t("contracts.history.version", { n: v.number }) }}</option>
              </NativeSelect>
            </FormField>
            <FormField :label="t('contracts.history.language')" for="history-language">
              <NativeSelect id="history-language" v-model="language" class="w-full" data-testid="history-language">
                <option v-for="l in languages" :key="l" :value="l">{{ LANGUAGE_NAMES[l] }}</option>
              </NativeSelect>
            </FormField>
          </div>
          <p class="text-muted-foreground m-0 text-xs" role="status" data-testid="history-diff-stats">{{ stats.added + stats.removed === 0 ? t("contracts.history.identical") : t("contracts.history.changes", { added: stats.added, removed: stats.removed }) }}</p>
          <div class="mono max-h-96 overflow-auto rounded-xl border text-xs" role="group" :aria-label="t('contracts.history.compare')" dir="ltr">
            <div v-for="(line, i) in lines" :key="i" :class="['flex gap-2 px-3 py-0.5', LINE[line.op]]" :data-op="line.op">
              <span aria-hidden="true" class="w-3 shrink-0 select-none">{{ MARK[line.op] }}</span>
              <span v-if="line.op !== 'same'" class="sr-only">{{ line.op === "add" ? t("contracts.history.added") : t("contracts.history.removed") }}</span>
              <span class="min-w-0 break-words whitespace-pre-wrap" dir="auto">{{ line.text }}</span>
            </div>
          </div>
        </section>
        <p v-else class="text-muted-foreground m-0 border-t pt-4 text-sm">{{ t("contracts.history.onlyOne") }}</p>
      </SheetBody>
    </SheetContent>
  </Sheet>
</template>
