<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { MAIL_CATEGORIES, useMailSettings, useSetMailCategory, type MailCategory } from "@/features/mail/api";
import { useApp, useMe } from "@/features/product/api";
import { tenantOf } from "@/shared/lib/environment";
import { firstError } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { StatusPill } from "@/shared/ui/status-pill";
import { Switch } from "@/shared/ui/switch";
import { useMailActions, useMailIntegration } from "./api";
import { NAFRU_EMAIL_URL } from "./brands";
import { builtInMailState, ownMailState } from "./status";

/**
 * The side sheet of the e-mail integrations. "Payrudder mail" is the built-in sender: there is nothing to connect, and the sheet holds the switches of which
 * kinds of mail are sent. "Nafru Email" is the tenant's own provider: a base URL, an API key and the address the mail comes from. The key is checked at the
 * provider before it is stored, is typed once and is never shown again; once it is connected, the live mail of the app goes through it. The sandbox's mail
 * always goes to the sandbox inbox.
 */
const props = defineProps<{ kind: "payrudderMail" | "nafru" | ""; appId: string; isOwner: boolean }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();

const app = useApp(() => props.appId);
const me = useMe();
const mail = useMailIntegration(() => props.appId, () => open.value);
const actions = useMailActions(() => props.appId);

// --- the built-in sender's switches (the tenant API, as the person)
const liveTenant = computed(() => (open.value && props.kind === "payrudderMail" ? tenantOf(app.data.value?.app ?? undefined, "live") : ""));
const settings = useMailSettings(liveTenant);
const setCategory = useSetMailCategory(liveTenant);
const enabled = computed(() => {
  const state = new Map<string, boolean>();
  for (const c of settings.data.value?.categories ?? []) if (c.category) state.set(c.category, c.enabled ?? true);
  return (category: MailCategory) => state.get(category) ?? true;
});

// --- the own provider
const baseUrl = ref("");
const apiKey = ref("");
const from = ref("");
const to = ref("");
const connection = computed(() => mail.data.value);
const own = computed(() => ownMailState(connection.value));
const builtIn = computed(() => builtInMailState(connection.value));
const stored = computed(() => connection.value?.integration);
const tested = computed(() => actions.test.data.value);

watch(
  () => [open.value, props.kind] as const,
  ([isOpen]) => {
    if (!isOpen) return;
    baseUrl.value = apiKey.value = "";
    from.value = "";
    to.value = me.data.value?.user?.email ?? "";
    actions.save.reset();
    actions.remove.reset();
    actions.test.reset();
  },
  { immediate: true },
);
// The stored address is the one to start from, when the person opens the sheet to change something.
watch(
  () => [open.value, stored.value?.base_url, stored.value?.from] as const,
  ([isOpen, url, f]) => {
    if (!isOpen || props.kind !== "nafru") return;
    if (!baseUrl.value && url) baseUrl.value = url;
    if (!from.value && f) from.value = f;
  },
  { immediate: true },
);

function submit() {
  if (props.kind !== "nafru" || !props.isOwner) return;
  actions.save.mutate(
    { baseUrl: baseUrl.value.trim(), apiKey: apiKey.value.trim(), from: from.value.trim() },
    {
      onSuccess: () => {
        apiKey.value = "";
        toast.success(t("product.integrations.saved", { name: "Nafru Email" }));
        open.value = false;
      },
    },
  );
}
function removeNow() {
  if (!window.confirm(t("product.integrations.mail.confirmRemove"))) return;
  actions.remove.mutate(undefined, { onSuccess: () => (open.value = false) });
}
const failure = computed(() => firstError(actions.save, actions.remove, actions.test, mail));
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="sm:max-w-xl" data-testid="mail-sheet">
      <template v-if="kind === 'payrudderMail'">
        <SheetHeader>
          <SheetTitle>{{ t("product.integrations.mail.builtInTitle") }}</SheetTitle>
          <SheetDescription>{{ t("product.integrations.mail.builtInIntro") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <p v-if="builtIn === 'unused'" class="bg-muted text-muted-foreground rounded-xl px-3 py-2 text-xs" data-testid="mail-unused-note">{{ t("product.integrations.mail.unusedNote") }}</p>
          <ul class="divide-y" :aria-label="t('billing.mail.title')">
            <li v-for="category in MAIL_CATEGORIES" :key="category" class="flex items-center justify-between gap-4 py-3">
              <label :for="`int-mail-${category}`" class="min-w-0 flex-1 cursor-pointer">
                <span class="block text-sm font-medium">{{ t(`billing.mail.categories.${category}`) }}</span>
                <span class="text-muted-foreground block text-xs">{{ t(`billing.mail.descriptions.${category}`) }}</span>
              </label>
              <Switch :id="`int-mail-${category}`" :data-testid="`int-mail-${category}`" :model-value="enabled(category)" :disabled="!isOwner || settings.isPending.value || setCategory.isPending.value" @update:model-value="setCategory.mutate({ category, enabled: $event })" />
            </li>
          </ul>
          <p class="text-muted-foreground text-xs">{{ t("billing.mail.providerNote") }}</p>
          <ErrorText :error="firstError(settings, setCategory)" />
        </SheetBody>
        <SheetFooter><Button variant="ghost" type="button" @click="open = false">{{ t("common.close") }}</Button></SheetFooter>
      </template>

      <template v-else-if="kind === 'nafru'">
        <SheetHeader>
          <SheetTitle>{{ t(own === "none" ? "product.integrations.connectTitle" : "product.integrations.manageTitle", { name: "Nafru Email" }) }}</SheetTitle>
          <SheetDescription>{{ t("product.integrations.mail.nafruIntro") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <p v-if="!isOwner" class="text-muted-foreground text-sm" data-testid="integration-owner-only">{{ t("product.integrations.ownerOnly") }}</p>
          <p class="bg-muted text-muted-foreground rounded-xl px-3 py-2 text-xs">{{ t("product.integrations.mail.envNote") }}</p>
          <p class="text-xs"><a :href="NAFRU_EMAIL_URL" target="_blank" rel="noopener noreferrer" class="text-primary font-medium hover:underline" data-testid="nafru-link">{{ t("product.integrations.mail.aboutNafru") }}</a></p>

          <div v-if="own !== 'none'" class="bg-muted flex flex-wrap items-center justify-between gap-3 rounded-2xl px-4 py-3" data-testid="integration-sheet-status">
            <div class="min-w-0">
              <StatusPill :tone="own === 'working' ? 'success' : 'danger'">{{ t(`product.integrations.state.${own}`) }}</StatusPill>
              <p class="text-muted-foreground mt-1 text-xs">{{ t("product.integrations.mail.keyLine", { hint: stored?.key_hint ?? "", from: stored?.from ?? "" }) }}</p>
              <p v-if="stored?.check_problem" class="text-destructive mt-1 text-xs">{{ t(`product.integrations.testFailed.${stored.check_problem}`) }}</p>
              <p v-if="tested" class="mt-1 text-xs" role="status" data-testid="integration-test-result" :class="tested.ok ? 'text-success' : 'text-destructive'">
                {{ tested.ok ? t("product.integrations.mail.testOk", { to }) : t(`product.integrations.testFailed.${tested.problem || "refused"}`) }}
              </p>
            </div>
          </div>
          <div v-if="own !== 'none' && isOwner" class="flex flex-wrap items-end gap-2">
            <div class="grid min-w-0 flex-1 gap-1">
              <label class="text-sm" for="mail-test-to">{{ t("product.integrations.mail.testTo") }}</label>
              <Input id="mail-test-to" v-model="to" type="email" data-testid="mail-test-to" />
            </div>
            <Button variant="outline" :disabled="actions.test.isPending.value || to.trim() === ''" data-testid="integration-test" @click="actions.test.mutate(to.trim())">{{ actions.test.isPending.value ? t("product.integrations.testing") : t("product.integrations.mail.sendTest") }}</Button>
          </div>

          <form id="mail-sheet-form" class="grid gap-3" novalidate autocomplete="off" :aria-label="t('product.integrations.connectTitle', { name: 'Nafru Email' })" @submit.prevent="submit">
            <p class="text-muted-foreground text-xs">{{ own !== "none" ? t("product.integrations.replaceNote") : t("product.integrations.secretsOnce") }}</p>
            <div class="grid gap-1">
              <label class="text-sm" for="mail-base">{{ t("product.integrations.mail.fields.base_url") }}</label>
              <Input id="mail-base" v-model="baseUrl" type="url" spellcheck="false" placeholder="https://" data-testid="mail-base-url" />
              <p class="text-muted-foreground text-xs">{{ t("product.integrations.mail.help.base_url") }}</p>
            </div>
            <div class="grid gap-1">
              <label class="text-sm" for="mail-key">{{ t("product.integrations.mail.fields.api_key") }}</label>
              <Input id="mail-key" v-model="apiKey" type="password" class="mono" spellcheck="false" autocomplete="new-password" data-testid="mail-api-key" />
            </div>
            <div class="grid gap-1">
              <label class="text-sm" for="mail-from">{{ t("product.integrations.mail.fields.from") }}</label>
              <Input id="mail-from" v-model="from" spellcheck="false" placeholder="Acme Billing <billing@acme.example>" data-testid="mail-from" />
              <p class="text-muted-foreground text-xs">{{ t("product.integrations.mail.help.from") }}</p>
            </div>
          </form>
          <ErrorText :error="failure" />
        </SheetBody>
        <SheetFooter>
          <Button v-if="isOwner && own !== 'none'" variant="ghost" class="text-destructive me-auto" type="button" :disabled="actions.remove.isPending.value" data-testid="integration-disconnect" @click="removeNow">{{ t("product.integrations.disconnect") }}</Button>
          <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="mail-sheet-form" :disabled="!isOwner || actions.save.isPending.value" data-testid="integration-connect">{{ actions.save.isPending.value ? t("product.integrations.checking") : t("product.integrations.mail.checkAndConnect") }}</Button>
        </SheetFooter>
      </template>
    </SheetContent>
  </Sheet>
</template>
