<script setup lang="ts">
import { Plus, Trash2 } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { useMe } from "@/features/product/api";
import { ACCOUNT_LABEL, type AppRow } from "@/features/providers/connections";
import { useCanWrite } from "@/shared/lib/access";
import { askConfirm } from "@/shared/lib/confirm";
import { firstError } from "@/shared/lib/errors";
import { readOf } from "@/shared/lib/reads";
import { Button } from "@/shared/ui/button";
import { Checkbox } from "@/shared/ui/checkbox";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useOrganisationMail, useOrganisationMailActions } from "./api";

/**
 * The e-mail accounts the organisation shares among its apps (ADR 0026): set up once here, used by as many apps as it is bound to. The key belongs to the organisation
 * and never reaches an app; an app lists the account as inherited and can make it its default or route a language to it. Owners and administrators write, every other
 * member reads. An account cannot be removed while an app uses it, so the button waits until the list of apps under it is empty. Only e-mail is shared: payment and
 * e-invoice accounts are still set up on each app.
 */
const props = defineProps<{ apps: readonly AppRow[] }>();
const { t } = useI18n();
const me = useMe();
const canWriteHere = useCanWrite();

const list = useOrganisationMail();
const read = computed(() => readOf(list));
const actions = useOrganisationMailActions();
const accounts = computed(() => list.data.value?.accounts ?? []);
const canWrite = computed(() => canWriteHere.value && list.data.value?.can_write === true);
const liveApps = computed(() => props.apps.filter((a) => a.hasLive));
const nameOf = (a: { label?: string }) => (a.label ? `Nafru Email (${a.label})` : "Nafru Email");

// --- binding: an app that does not use the account yet, and whether it becomes that app's default
const pick = ref<Record<string, string>>({});
const asDefault = ref<Record<string, boolean>>({});
const free = (usedBy: readonly { app_id: string }[]) => liveApps.value.filter((a) => !usedBy.some((u) => u.app_id === a.id));
function bind(account: string) {
  const appId = pick.value[account];
  if (!appId) return;
  actions.bind.mutate({ account, appId, makeDefault: asDefault.value[account] === true }, { onSuccess: () => ((pick.value = { ...pick.value, [account]: "" }), (asDefault.value = { ...asDefault.value, [account]: false })) });
}
async function unbind(account: string, appId: string, appName: string, display: string) {
  if (!(await askConfirm({ body: t("product.integrations.orgMail.confirmStop", { app: appName, name: display }) }))) return;
  actions.unbind.mutate({ account, appId });
}
async function remove(account: string, display: string) {
  if (!(await askConfirm({ body: t("product.integrations.orgMail.confirmRemove", { name: display }) }))) return;
  actions.remove.mutate(account);
}
const tested = ref<Record<string, boolean>>({});
function test(account: string) {
  actions.test.mutate({ account, to: me.data.value?.user?.email ?? "" }, { onSuccess: (r) => (tested.value = { ...tested.value, [account]: r.ok === true }) });
}

// --- the sheet: add an account, or replace the key of one (its label stays)
const open = ref(false);
const editing = ref("");
const label = ref("");
const baseUrl = ref("");
const apiKey = ref("");
const from = ref("");
const labelProblem = computed(() => editing.value === "" && (label.value.trim() === "" ? accounts.value.length > 0 : !ACCOUNT_LABEL.test(label.value.trim())));
watch(open, (v) => {
  if (!v) return;
  const a = accounts.value.find((x) => x.account === editing.value);
  label.value = a?.label ?? "";
  baseUrl.value = a?.base_url ?? "";
  from.value = a?.from ?? "";
  apiKey.value = "";
  actions.save.reset();
});
function openSheet(account: string) {
  editing.value = account;
  open.value = true;
}
function submit() {
  if (!canWrite.value || labelProblem.value) return;
  actions.save.mutate(
    { baseUrl: baseUrl.value.trim(), apiKey: apiKey.value.trim(), from: from.value.trim(), label: label.value.trim() },
    {
      onSuccess: (r) => {
        apiKey.value = "";
        toast.success(t("product.integrations.orgMail.saved", { name: nameOf(r) }));
        open.value = false;
      },
    },
  );
}
const failure = computed(() => firstError(actions.remove, actions.bind, actions.unbind, actions.test));
</script>

<template>
  <section class="surface flex flex-col gap-4 p-5" data-testid="org-mail-card" aria-labelledby="org-mail-title">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h2 id="org-mail-title" class="text-lg font-semibold tracking-tight">{{ t("product.integrations.orgMail.title") }}</h2>
        <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.integrations.orgMail.intro") }}</p>
      </div>
      <Button v-if="canWrite" variant="outline" size="sm" data-testid="org-mail-add" @click="openSheet('')"><Plus class="size-4" />{{ t("product.integrations.orgMail.add") }}</Button>
    </div>

    <Skeleton v-if="read.loading" class="h-24 rounded-2xl" />
    <ErrorState v-else-if="read.error" :error="read.error" :retrying="read.fetching" compact class="mx-0" @retry="read.retry()" />
    <template v-else>
      <p v-if="!canWrite" class="text-muted-foreground text-sm" data-testid="org-mail-read-only">{{ t("product.integrations.orgMail.readOnly") }}</p>
      <p v-if="accounts.length === 0" class="text-muted-foreground text-sm" data-testid="org-mail-empty">{{ t("product.integrations.orgMail.empty") }}</p>
      <ul v-else class="divide-border flex flex-col divide-y" :aria-label="t('product.integrations.orgMail.title')">
        <li v-for="a in accounts" :key="a.account" class="flex flex-col gap-3 py-4" :data-testid="`org-mail-${a.account}`">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="flex min-w-0 flex-wrap items-center gap-2">
              <span class="font-medium">Nafru Email</span>
              <span v-if="a.label" class="mono bg-muted rounded-md px-1.5 py-0.5 text-xs">{{ a.label }}</span>
              <StatusPill :tone="a.check_problem ? 'danger' : 'success'" :data-testid="`org-mail-state-${a.account}`">{{ t(`product.integrations.state.${a.check_problem ? "problem" : "working"}`) }}</StatusPill>
              <span class="text-muted-foreground text-xs">{{ t("product.integrations.mail.keyLine", { hint: a.key_hint, from: a.from }) }}</span>
            </div>
            <div v-if="canWrite" class="flex flex-wrap items-center gap-2">
              <Button variant="ghost" size="sm" :disabled="actions.test.isPending.value" :data-testid="`org-mail-test-${a.account}`" @click="test(a.account)">{{ t("product.integrations.orgMail.test") }}</Button>
              <Button variant="outline" size="sm" :data-testid="`org-mail-replace-${a.account}`" @click="openSheet(a.account)">{{ t("product.providers.manage") }}</Button>
              <Button variant="ghost" size="sm" class="text-destructive" :disabled="a.used_by.length > 0 || actions.remove.isPending.value" :data-testid="`org-mail-remove-${a.account}`" @click="remove(a.account, nameOf(a))">
                <Trash2 class="size-4" />{{ t("product.integrations.orgMail.remove") }}
              </Button>
            </div>
          </div>
          <p v-if="tested[a.account] !== undefined" class="text-xs" role="status" :class="tested[a.account] ? 'text-success' : 'text-destructive'" :data-testid="`org-mail-test-result-${a.account}`">
            {{ tested[a.account] ? t("product.integrations.orgMail.testOk", { to: me.data.value?.user?.email ?? "" }) : t("product.integrations.testFailed.refused") }}
          </p>
          <p v-if="a.check_problem" class="text-destructive text-xs">{{ t(`product.integrations.testFailed.${a.check_problem}`) }}</p>

          <p v-if="a.used_by.length === 0" class="text-muted-foreground text-sm" :data-testid="`org-mail-unused-${a.account}`">{{ t("product.integrations.orgMail.unused") }}</p>
          <ul v-else class="flex flex-wrap gap-2" :aria-label="t('product.integrations.orgMail.usedBy', { apps: '' })">
            <li v-for="u in a.used_by" :key="u.app_id" class="bg-muted flex items-center gap-2 rounded-full py-1 ps-3 pe-1 text-sm" :data-testid="`org-mail-app-${a.account}-${u.app_id}`">
              <span>{{ u.name }}</span>
              <StatusPill v-if="u.is_default" tone="info">{{ t("product.providers.account.default") }}</StatusPill>
              <Button v-if="canWrite" variant="ghost" size="sm" :disabled="actions.unbind.isPending.value" :data-testid="`org-mail-unbind-${a.account}-${u.app_id}`" @click="unbind(a.account, u.app_id, u.name, nameOf(a))">{{ t("product.integrations.orgMail.stop") }}</Button>
            </li>
          </ul>

          <div v-if="canWrite" class="flex flex-wrap items-end gap-3">
            <p v-if="free(a.used_by).length === 0" class="text-muted-foreground text-xs">{{ t("product.integrations.orgMail.noApps") }}</p>
            <template v-else>
              <div class="grid gap-1">
                <label class="text-xs" :for="`org-mail-pick-${a.account}`">{{ t("product.integrations.orgMail.useIn") }}</label>
                <NativeSelect :id="`org-mail-pick-${a.account}`" v-model="pick[a.account]" :data-testid="`org-mail-pick-${a.account}`">
                  <option value="">{{ t("product.integrations.orgMail.app") }}</option>
                  <option v-for="p in free(a.used_by)" :key="p.id" :value="p.id">{{ p.name }}</option>
                </NativeSelect>
              </div>
              <label class="flex items-center gap-2 text-sm"><Checkbox v-model="asDefault[a.account]" :aria-label="t('product.integrations.orgMail.makeDefault')" :data-testid="`org-mail-default-${a.account}`" />{{ t("product.integrations.orgMail.makeDefault") }}</label>
              <Button size="sm" :disabled="!pick[a.account] || actions.bind.isPending.value" :data-testid="`org-mail-bind-${a.account}`" @click="bind(a.account)">{{ t("product.integrations.orgMail.use") }}</Button>
            </template>
          </div>
        </li>
      </ul>
      <ErrorText :error="failure" />
    </template>

    <Sheet v-model:open="open">
      <SheetContent class="sm:max-w-xl" data-testid="org-mail-sheet">
        <SheetHeader>
          <SheetTitle>{{ t("product.integrations.orgMail.sheetTitle") }}</SheetTitle>
          <SheetDescription>{{ t("product.integrations.orgMail.sheetIntro") }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <form id="org-mail-form" class="grid gap-3" novalidate autocomplete="off" @submit.prevent="submit">
            <div v-if="editing === ''" class="grid gap-1">
              <label class="text-sm" for="org-mail-label">{{ t("product.providers.account.label") }}<span v-if="accounts.length === 0" class="text-muted-foreground"> ({{ t("product.integrations.optional") }})</span></label>
              <Input id="org-mail-label" v-model="label" class="mono" spellcheck="false" autocomplete="off" :aria-invalid="labelProblem" data-testid="org-mail-label" />
              <p class="text-muted-foreground text-xs">{{ t("product.providers.account.labelHelp") }}</p>
              <p v-if="labelProblem" class="text-destructive text-xs" role="alert">{{ t("product.providers.account.labelBad") }}</p>
            </div>
            <div class="grid gap-1">
              <label class="text-sm" for="org-mail-base">{{ t("product.integrations.mail.fields.base_url") }}</label>
              <Input id="org-mail-base" v-model="baseUrl" type="url" spellcheck="false" placeholder="https://" data-testid="org-mail-base-url" />
              <p class="text-muted-foreground text-xs">{{ t("product.integrations.mail.help.base_url") }}</p>
            </div>
            <div class="grid gap-1">
              <label class="text-sm" for="org-mail-key">{{ t("product.integrations.mail.fields.api_key") }}</label>
              <Input id="org-mail-key" v-model="apiKey" type="password" class="mono" spellcheck="false" autocomplete="new-password" data-testid="org-mail-api-key" />
            </div>
            <div class="grid gap-1">
              <label class="text-sm" for="org-mail-from">{{ t("product.integrations.mail.fields.from") }}</label>
              <Input id="org-mail-from" v-model="from" spellcheck="false" placeholder="Acme Billing <billing@acme.example>" data-testid="org-mail-from" />
              <p class="text-muted-foreground text-xs">{{ t("product.integrations.mail.help.from") }}</p>
            </div>
          </form>
          <ErrorText :error="firstError(actions.save)" />
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="org-mail-form" :disabled="!canWrite || actions.save.isPending.value || labelProblem" data-testid="org-mail-save">{{ actions.save.isPending.value ? t("product.integrations.checking") : t("product.integrations.mail.checkAndConnect") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  </section>
</template>
