<script setup lang="ts">
import { useDebounceFn } from "@vueuse/core";
import { TriangleAlert } from "@lucide/vue";
import { TabsContent } from "reka-ui";
import { computed, nextTick, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { askConfirm } from "@/shared/lib/confirm";
import { toApiLocale, type ApiLocale } from "@/shared/lib/locale";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Switch } from "@/shared/ui/switch";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import { Textarea } from "@/shared/ui/textarea";
import { usePreviewContract, usePublishVersion, useStarterText } from "./api";
import { LANGUAGE_NAMES, LANGUAGES, STARTER_LANGUAGES, VARIABLES, hasTemplateNotice, insertAtCursor, previewDocument, publishFacts, versionsOf, type Audience, type ContractSettings, type ContractVersion, type Kind } from "./lib";
import SandboxedHtml from "./SandboxedHtml.vue";

/**
 * The editor of an agreement: its text in each language (a tab each), the variables to put in it, a preview of every language as a customer sees it,
 * and what publishing will do, said before it is done. A version cannot be edited, so "edit" opens the newest one as the draft of the next. The first
 * version of a series is always material.
 */
const props = defineProps<{ tenantId: string; series: { kind: Kind; audience: Audience } | null; versions: ContractVersion[]; settings?: ContractSettings; loaded: boolean }>();
const open = defineModel<boolean>("open", { required: true });
const { t, locale } = useI18n();
const canWrite = useCanWrite();

const current = computed(() => (props.series ? versionsOf(props.versions, props.series.kind, props.series.audience)[0] : undefined));
const first = computed(() => current.value === undefined);
const next = computed(() => (current.value?.number ?? 0) + 1);
const kindName = computed(() => (props.series ? t(`contracts.kinds.${props.series.kind}`) : ""));
const audienceName = computed(() => (props.series ? t(`contracts.audiences.${props.series.audience}`) : ""));

const bodies = ref<Record<string, string>>({});
const language = ref<ApiLocale>("en");
const material = ref(true);
const note = ref("");
const confirming = ref(false);
/** A variable as it is written in a text. */
const token = (name: string): string => `{{${name}}}`;
const textOf = (l: string): string => bodies.value[l] ?? "";
const filled = (l: string): boolean => textOf(l).trim() !== "";

const publish = usePublishVersion(() => props.tenantId);
const preview = usePreviewContract(() => props.tenantId);
const starter = useStarterText(() => props.tenantId);
const previews = ref<Record<string, { text: string; html: string; unknown: string[] }>>({});
const previewError = ref<unknown>(null);

/** Every language that has a text is rendered by the server a moment after the last key, so the preview, the variables it uses and the unknown ones are always those of the text on screen. */
const check = useDebounceFn(async () => {
  await Promise.all(
    LANGUAGES.map(async (l) => {
      const text = textOf(l);
      if (text.trim() === "") {
        delete previews.value[l];
        return;
      }
      if (previews.value[l]?.text === text) return;
      try {
        const r = await preview.mutateAsync({ body: text, locale: l });
        if (textOf(l) === text) previews.value[l] = { text, html: r.html ?? "", unknown: r.unknown_variables ?? [] };
        previewError.value = null;
      } catch (e) {
        previewError.value = e;
      }
    }),
  );
}, 400);
// A sheet that opens starts from the newest version (the draft of the next one) and the last values, previews and errors are not shown again. It waits for the versions to arrive.
watch(
  () => `${open.value && props.loaded}:${props.series?.kind}:${props.series?.audience}`,
  () => {
    if (!open.value || !props.loaded) return;
    bodies.value = { ...(current.value?.bodies ?? {}) };
    const mine = toApiLocale(locale.value);
    language.value = filled(mine) ? mine : (LANGUAGES.find(filled) ?? mine);
    material.value = true;
    note.value = "";
    confirming.value = false;
    previews.value = {};
    previewError.value = null;
    publish.reset();
    starter.reset();
    void check();
  },
  { immediate: true },
);
watch(language, () => starter.reset());

watch(bodies, () => void check(), { deep: true });

const checking = computed(() => LANGUAGES.some((l) => filled(l) && previews.value[l]?.text !== textOf(l)));
const unknown = computed(() => LANGUAGES.filter((l) => filled(l) && previews.value[l]?.text === textOf(l) && (previews.value[l]?.unknown.length ?? 0) > 0).map((l) => ({ language: l, names: previews.value[l]?.unknown ?? [] })));
const notices = computed(() => LANGUAGES.filter((l) => filled(l) && hasTemplateNotice(textOf(l))));
const canPublish = computed(() => canWrite.value && props.series !== null && LANGUAGES.some(filled) && unknown.value.length === 0 && !checking.value && !publish.isPending.value);
const required = computed(() => (props.series ? (props.settings?.required_kinds.includes(props.series.kind) ?? false) : false));
const facts = computed(() => publishFacts({ first: first.value, material: first.value || material.value, required: required.value, template: notices.value.length > 0 }));

// --- the text
const editor = ref<{ $el: HTMLTextAreaElement } | null>(null);
function insert(name: string) {
  const el = editor.value?.$el;
  const text = textOf(language.value);
  const done = insertAtCursor(text, el?.selectionStart ?? text.length, el?.selectionEnd ?? text.length, name);
  bodies.value = { ...bodies.value, [language.value]: done.text };
  void nextTick(() => {
    el?.focus();
    el?.setSelectionRange(done.caret, done.caret);
  });
}
const canStarter = computed(() => (STARTER_LANGUAGES as readonly string[]).includes(language.value));
async function useStarter() {
  const l = language.value;
  if (!props.series || !canStarter.value) return;
  if (filled(l) && !(await askConfirm({ title: t("contracts.editor.starterReplaceTitle", { language: LANGUAGE_NAMES[l] }), body: t("contracts.editor.starterReplace", { language: LANGUAGE_NAMES[l] }), confirmLabel: t("contracts.editor.starterReplaceAction"), destructive: false }))) return;
  const got = await starter.mutateAsync({ kind: props.series.kind, audience: props.series.audience, locale: l as "tr" | "en" }).catch(() => undefined);
  if (got?.body) bodies.value = { ...bodies.value, [l]: got.body };
}

// --- publishing
function submit() {
  const series = props.series;
  if (!series) return;
  const texts = Object.fromEntries(LANGUAGES.filter(filled).map((l) => [l, textOf(l)]));
  publish.mutate(
    { kind: series.kind, audience: series.audience, bodies: texts, material: first.value || material.value, note: note.value.trim() },
    {
      onSuccess: () => {
        confirming.value = false;
        open.value = false;
      },
    },
  );
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="sm:max-w-3xl" data-testid="version-sheet">
      <SheetHeader>
        <SheetTitle>{{ t("contracts.editor.title", { kind: kindName, audience: audienceName }) }}</SheetTitle>
        <SheetDescription>{{ first ? t("contracts.editor.descriptionFirst") : t("contracts.editor.descriptionNext", { n: next, current: current?.number ?? 0 }) }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="version-form" class="grid gap-5" role="form" novalidate :aria-label="t('contracts.editor.formLabel')" @submit.prevent="confirming = canPublish">
          <Tabs :model-value="language" class="gap-3" @update:model-value="language = $event as ApiLocale">
            <TabsList class="h-auto w-full flex-wrap justify-start" :aria-label="t('contracts.editor.languages')">
              <TabsTrigger v-for="l in LANGUAGES" :key="l" :value="l" class="flex-none gap-1.5" :data-testid="`version-lang-${l}`">
                <span :lang="l">{{ LANGUAGE_NAMES[l] }}</span>
                <span v-if="filled(l)" class="bg-success size-1.5 rounded-full" aria-hidden="true" />
                <span v-if="filled(l)" class="sr-only">{{ t("contracts.editor.hasText") }}</span>
              </TabsTrigger>
            </TabsList>

            <TabsContent :value="language" class="grid gap-4 outline-none">
              <div class="flex flex-wrap items-center gap-2">
                <Button type="button" size="sm" variant="outline" :disabled="!canStarter || !canWrite || starter.isPending.value" data-testid="version-starter" @click="useStarter">{{ t("contracts.editor.starter") }}</Button>
                <span v-if="!canStarter" class="text-muted-foreground text-xs" data-testid="version-starter-only">{{ t("contracts.editor.starterOnly") }}</span>
                <span v-else class="text-muted-foreground text-xs">{{ t("contracts.editor.starterNote") }}</span>
              </div>
              <ErrorText :error="starter.error.value" />

              <FormField :label="t('contracts.editor.textLabel', { language: LANGUAGE_NAMES[language] })" for="version-text" :hint="t('contracts.editor.format')">
                <Textarea
                  id="version-text"
                  ref="editor"
                  :model-value="textOf(language)"
                  :lang="language"
                  dir="auto"
                  maxlength="60000"
                  class="mono min-h-64 text-sm"
                  :placeholder="t('contracts.editor.placeholder')"
                  data-testid="version-text"
                  @update:model-value="bodies = { ...bodies, [language]: String($event) }"
                />
              </FormField>

              <ErrorText :error="previewError" />
              <div v-if="unknown.length > 0" class="bg-destructive-soft text-destructive flex items-start gap-2 rounded-2xl px-4 py-3 text-sm" role="alert" data-testid="version-unknown">
                <TriangleAlert class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                <ul class="m-0 list-none p-0">
                  <li v-for="u in unknown" :key="u.language">{{ t("contracts.editor.unknown", { language: LANGUAGE_NAMES[u.language], names: u.names.join(", ") }) }}</li>
                </ul>
              </div>
              <div v-if="notices.length > 0" class="bg-warning-soft text-warning flex items-start gap-2 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="version-notice">
                <TriangleAlert class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                <ul class="m-0 list-none p-0">
                  <li v-for="l in notices" :key="l">{{ t("contracts.editor.noticeStill", { language: LANGUAGE_NAMES[l] }) }}</li>
                </ul>
              </div>

              <div class="grid gap-1.5" role="group" :aria-label="t('contracts.editor.variables')">
                <span class="text-sm font-medium">{{ t("contracts.editor.variables") }}</span>
                <p class="text-muted-foreground text-xs">{{ t("contracts.editor.variablesHint") }}</p>
                <div class="flex flex-wrap gap-1.5">
                  <button
                    v-for="v in VARIABLES"
                    :key="v"
                    type="button"
                    class="bg-muted hover:bg-accent mono rounded-lg px-2 py-1 text-xs"
                    :aria-label="t('contracts.editor.insert', { name: v })"
                    :data-testid="`variable-${v}`"
                    @click="insert(v)"
                  >
                    {{ token(v) }}
                  </button>
                </div>
              </div>

              <div class="grid gap-1.5" data-testid="version-preview">
                <span class="text-sm font-medium">{{ t("contracts.editor.preview", { language: LANGUAGE_NAMES[language] }) }}</span>
                <p class="text-muted-foreground text-xs">{{ t("contracts.editor.previewHint") }}</p>
                <div class="h-80">
                  <SandboxedHtml v-if="previews[language] && previews[language]?.text === textOf(language)" :doc="previewDocument(previews[language]?.html ?? '', language)" :title="t('contracts.editor.preview', { language: LANGUAGE_NAMES[language] })" />
                  <p v-else class="text-muted-foreground bg-muted/50 flex h-full items-center justify-center rounded-xl p-4 text-center text-sm">{{ filled(language) ? t("contracts.editor.previewing") : t("contracts.editor.previewEmpty") }}</p>
                </div>
              </div>
            </TabsContent>
          </Tabs>

          <div class="flex items-start justify-between gap-4">
            <label for="version-material" class="min-w-0 flex-1 cursor-pointer">
              <span class="block text-sm font-medium">{{ t("contracts.editor.material") }}</span>
              <span class="text-muted-foreground block text-xs">{{ first ? t("contracts.editor.materialFirst") : t("contracts.editor.materialHint") }}</span>
            </label>
            <Switch id="version-material" :model-value="first || material" :disabled="first || !canWrite" data-testid="version-material" @update:model-value="material = $event" />
          </div>

          <FormField :label="t('contracts.editor.note')" for="version-note" :hint="t('contracts.editor.noteHint')">
            <Input id="version-note" v-model="note" maxlength="500" data-testid="version-note" />
          </FormField>
          <ErrorText :error="publish.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <p v-if="!LANGUAGES.some(filled)" class="text-muted-foreground me-auto self-center text-xs">{{ t("contracts.editor.needText") }}</p>
        <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="version-form" :disabled="!canPublish" data-testid="version-publish">{{ t("contracts.editor.publish", { n: next }) }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <!-- What publishing does, said before it is done. -->
  <Dialog v-model:open="confirming">
    <DialogContent class="sm:max-w-lg" data-testid="publish-confirm">
      <DialogHeader>
        <DialogTitle>{{ t("contracts.confirm.title", { n: next, kind: kindName }) }}</DialogTitle>
        <DialogDescription>{{ t("contracts.confirm.description") }}</DialogDescription>
      </DialogHeader>
      <ul class="m-0 grid list-disc gap-2 ps-5 text-sm" data-testid="publish-facts">
        <li v-for="f in facts" :key="f" :data-fact="f">{{ t(`contracts.confirm.${f}`, { kind: kindName, audience: audienceName }) }}</li>
      </ul>
      <ErrorText :error="publish.error.value" />
      <DialogFooter>
        <Button type="button" variant="ghost" @click="confirming = false">{{ t("common.cancel") }}</Button>
        <Button type="button" :disabled="publish.isPending.value" data-testid="publish-confirm-accept" @click="submit">{{ t("contracts.confirm.action", { n: next }) }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
