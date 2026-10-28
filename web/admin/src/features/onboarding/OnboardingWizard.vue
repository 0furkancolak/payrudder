<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ArrowRight, Check, FileText, Gauge, Tag, Wallet } from "@lucide/vue";
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import LanguageSwitcher from "@/app/layout/LanguageSwitcher.vue";
import { useAccount, useApps, useCreateApp, useRenameAccount } from "@/features/product/api";
import { currencyOfZone, FALLBACK_CURRENCY, type Currency } from "@/shared/lib/currencies";
import { tenantOf } from "@/shared/lib/environment";
import { ownZone } from "@/shared/lib/intl";
import { BrandLogo } from "@/shared/ui/brand-logo";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { CurrencySelect, TimezoneSelect } from "@/shared/ui/search-select";
import { markTourSeen } from "@/shared/ui/tour";
import { finishOnboarding, patchProgress, progress, skipOnboarding, type Step } from "./state";

/**
 * The first-run wizard: three screens. It says what Payrudder is for, makes the organisation's first app (with its sandbox) from one short form, and points at
 * what comes next. Meters, packages and keys are not asked for here: the "Getting started" card on the home offers each of them, ticked from the person's
 * data. Progress is stored per person (state.ts), so closing the tab and coming back continues where it stopped.
 */
const { t } = useI18n();
const account = useAccount();
const apps = useApps();
const renameAccount = useRenameAccount();
const createApp = useCreateApp();

const step = computed<Step>(() => progress.value?.step ?? "welcome");
const app = computed(() => apps.data.value?.apps?.find((a) => a.id === progress.value?.app));
const sandbox = computed(() => tenantOf(app.value, "sandbox"));

const orgName = ref("");
// A new account starts on the currency of the time zone the browser is in (a person in Istanbul does not start on USD); a stored choice wins.
const orgZone = ref(progress.value?.timezone ?? ownZone());
const orgCurrency = ref<Currency>((progress.value?.currency as Currency | undefined) ?? currencyOfZone(orgZone.value) ?? FALLBACK_CURRENCY);
const appName = ref(t("onboarding.setup.defaultAppName"));

const isOwner = computed(() => account.data.value?.role === "owner");
watch(() => account.data.value?.name, (n) => { if (orgName.value === "") orgName.value = n ?? ""; }, { immediate: true });
const busy = computed(() => renameAccount.isPending.value || createApp.isPending.value);

const heading = ref<HTMLElement | null>(null);
// A new step is announced by moving the focus to its heading.
watch(step, async () => {
  await nextTick();
  heading.value?.focus();
});
onMounted(() => heading.value?.focus());

/** The wizard has said what the home's tour would say, so the tour is not started on top of it; the tour button still opens it. */
function finish() {
  markTourSeen("overview");
  finishOnboarding();
}

const start = () => patchProgress({ status: "active", step: "app" });
const back = () => patchProgress({ step: "welcome" });

async function save() {
  try {
    if (isOwner.value && orgName.value.trim() !== "" && orgName.value.trim() !== account.data.value?.name) await renameAccount.mutateAsync(orgName.value.trim());
    // Coming back to this step after the app was made does not make another.
    const made = app.value ?? (await createApp.mutateAsync({ name: appName.value.trim(), currency: orgCurrency.value, timezone: orgZone.value }));
    patchProgress({ app: made.id, currency: orgCurrency.value, timezone: orgZone.value, step: "done" });
  } catch {
    // The error is on the mutation, under the form.
  }
}

const FLOW = [
  { key: "meter", icon: Gauge },
  { key: "price", icon: Tag },
  { key: "bill", icon: FileText },
  { key: "paid", icon: Wallet },
] as const;
/** Where the person goes from the last screen: the things the first app needs next, in the order the work goes. */
const NEXT = computed(() => [
  { key: "meters", to: `/app/t/${sandbox.value}/meters`, search: {} },
  { key: "packages", to: `/app/t/${sandbox.value}/packages`, search: {} },
  { key: "keys", to: `/app/apps/${progress.value?.app ?? ""}/keys`, search: { env: "sandbox" } },
  { key: "live", to: `/app/apps/${progress.value?.app ?? ""}/go-live`, search: { env: "live" } },
]);
</script>

<template>
  <div class="bg-background fixed inset-0 z-[80] overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="onb-title" data-testid="onboarding">
    <header class="mx-auto flex max-w-3xl items-center justify-between gap-3 px-4 pt-5 md:px-6">
      <BrandLogo />
      <div class="flex items-center gap-2">
        <LanguageSwitcher />
        <Button v-if="step !== 'done'" type="button" variant="ghost" data-testid="onb-skip" @click="skipOnboarding()">{{ t("onboarding.skip") }}</Button>
      </div>
    </header>

    <div class="mx-auto flex max-w-3xl flex-col gap-6 px-4 pt-6 pb-16 md:px-6">
      <section class="surface flex flex-col gap-6 p-6 md:p-9" :aria-label="t(`onboarding.steps.${step}`)">
        <!-- Why Payrudder exists -->
        <template v-if="step === 'welcome'">
          <div class="flex flex-col gap-2">
            <h1 id="onb-title" ref="heading" tabindex="-1" class="text-3xl font-semibold tracking-tight outline-none md:text-4xl">{{ t("onboarding.welcome.title") }}</h1>
            <p class="text-muted-foreground max-w-xl text-base">{{ t("onboarding.welcome.body") }}</p>
          </div>
          <ol class="grid gap-3 sm:grid-cols-2" data-testid="onb-flow">
            <li v-for="(f, i) in FLOW" :key="f.key" class="bg-muted/60 flex items-start gap-3 rounded-2xl p-4">
              <span class="bg-brand-soft text-brand-strong flex size-10 shrink-0 items-center justify-center rounded-xl"><component :is="f.icon" class="size-5" aria-hidden="true" /></span>
              <span class="flex flex-col gap-0.5">
                <span class="text-muted-foreground text-xs font-medium tabular-nums">{{ i + 1 }}</span>
                <span class="font-semibold">{{ t(`onboarding.welcome.flow.${f.key}.title`) }}</span>
                <span class="text-muted-foreground text-sm">{{ t(`onboarding.welcome.flow.${f.key}.body`) }}</span>
              </span>
            </li>
          </ol>
          <div class="flex flex-wrap items-center gap-3">
            <Button size="lg" data-testid="onb-start" @click="start"><ArrowRight class="size-4 rtl:rotate-180" aria-hidden="true" />{{ t("onboarding.welcome.start") }}</Button>
            <span class="text-muted-foreground text-sm">{{ t("onboarding.welcome.time") }}</span>
          </div>
        </template>

        <!-- Organisation and first app, in one form -->
        <form v-else-if="step === 'app'" class="flex flex-col gap-5" @submit.prevent="save">
          <div class="flex flex-col gap-1.5">
            <h1 id="onb-title" ref="heading" tabindex="-1" class="text-2xl font-semibold tracking-tight outline-none">{{ t("onboarding.setup.title") }}</h1>
            <p class="text-muted-foreground text-sm">{{ t("onboarding.setup.body") }}</p>
          </div>
          <p v-if="app" class="bg-success-soft text-success flex items-center gap-2 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="onb-app-made">
            <Check class="size-4 shrink-0" aria-hidden="true" />{{ t("onboarding.setup.created", { name: app.name }) }}
          </p>
          <template v-else>
            <div v-if="isOwner" class="flex flex-col gap-1.5">
              <Label for="onb-org">{{ t("onboarding.setup.org") }}</Label>
              <Input id="onb-org" v-model="orgName" autocomplete="organization" maxlength="200" required data-testid="onb-org-name" />
            </div>
            <div class="flex flex-col gap-1.5">
              <Label for="onb-app">{{ t("onboarding.setup.app") }}</Label>
              <Input id="onb-app" v-model="appName" autocomplete="off" maxlength="100" required data-testid="onb-app-name" />
              <p class="text-muted-foreground text-xs">{{ t("onboarding.setup.appHint") }}</p>
            </div>
            <div class="grid gap-4 sm:grid-cols-2">
              <div class="flex flex-col gap-1.5">
                <Label for="onb-currency">{{ t("onboarding.setup.currency") }}</Label>
                <CurrencySelect id="onb-currency" v-model="orgCurrency" :aria-label="t('onboarding.setup.currency')" />
              </div>
              <div class="flex flex-col gap-1.5">
                <Label for="onb-zone">{{ t("onboarding.setup.timezone") }}</Label>
                <TimezoneSelect id="onb-zone" v-model="orgZone" :aria-label="t('onboarding.setup.timezone')" />
              </div>
            </div>
            <p class="text-muted-foreground text-xs">{{ t("onboarding.setup.note") }}</p>
          </template>
          <ErrorText :error="renameAccount.error.value" />
          <ErrorText :error="createApp.error.value" />
          <div class="flex items-center justify-between gap-3">
            <Button type="button" variant="ghost" :disabled="!!app" @click="back">{{ t("onboarding.back") }}</Button>
            <Button type="submit" :disabled="busy || (!app && (appName.trim() === '' || (isOwner && orgName.trim() === '')))" data-testid="onb-continue">{{ app ? t("onboarding.continue") : t("onboarding.setup.create") }}</Button>
          </div>
        </form>

        <!-- Done -->
        <template v-else>
          <div class="flex flex-col gap-1.5">
            <span class="bg-success-soft text-success flex size-12 items-center justify-center rounded-2xl"><Check class="size-6" aria-hidden="true" /></span>
            <h1 id="onb-title" ref="heading" tabindex="-1" class="mt-2 text-2xl font-semibold tracking-tight outline-none">{{ t("onboarding.done.title") }}</h1>
            <p class="text-muted-foreground text-sm">{{ t("onboarding.done.body") }}</p>
          </div>
          <ul class="flex flex-col gap-2" data-testid="onb-next">
            <li v-for="n in NEXT" :key="n.key">
              <Link :to="n.to" :search="n.search as never" class="hover:bg-muted flex items-center justify-between gap-3 rounded-2xl border px-4 py-3 text-sm" @click="finish">
                <span><span class="font-medium">{{ t(`onboarding.done.next.${n.key}.title`) }}</span><span class="text-muted-foreground block">{{ t(`onboarding.done.next.${n.key}.body`) }}</span></span>
                <ArrowRight class="size-4 shrink-0 rtl:rotate-180" aria-hidden="true" />
              </Link>
            </li>
          </ul>
          <div class="flex justify-end">
            <Button size="lg" data-testid="onb-finish" @click="finish">{{ t("onboarding.done.finish") }}</Button>
          </div>
        </template>
      </section>
    </div>
  </div>
</template>
