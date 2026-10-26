<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ArrowRight, Check, FileText, Gauge, Tag, Wallet } from "@lucide/vue";
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import LanguageSwitcher from "@/app/layout/LanguageSwitcher.vue";
import { useCreateMeter, useMeters } from "@/features/meters/api";
import { useCreatePackage } from "@/features/packages/api";
import { buildUsage, newPrice } from "@/features/packages/usage";
import { useAccount, useApps, useCreateApp, useRenameAccount } from "@/features/product/api";
import { FALLBACK_CURRENCY, type Currency } from "@/shared/lib/currencies";
import { tenantOf } from "@/shared/lib/environment";
import { ownZone } from "@/shared/lib/intl";
import { parseMajor } from "@/shared/lib/money";
import { BrandLogo } from "@/shared/ui/brand-logo";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { CurrencySelect, TimezoneSelect } from "@/shared/ui/search-select";
import { useSendTestEvent } from "./api";
import { finishOnboarding, patchProgress, progress, skipOnboarding, slug, stepBy, type Step } from "./state";
import { METER_TEMPLATES, templateByCode, TEST_CUSTOMER, type MeterTemplate } from "./templates";

/**
 * The first-run wizard: a screen of its own that says what Payrudder is for and does the first setup in the same flow (organisation, first app, first
 * meter, first package, a test event in the sandbox). What it makes is real: the app and its sandbox, a meter, a package. Progress is stored per person
 * after every step (state.ts), so closing the tab and coming back continues where it stopped, and "Skip for now" hands over to the checklist on the home.
 */
const { t } = useI18n();
const account = useAccount();
const apps = useApps();
const renameAccount = useRenameAccount();
const createApp = useCreateApp();

const step = computed<Step>(() => progress.value?.step ?? "welcome");
/** The steps that do something, numbered for the progress bar (the welcome and the last screen are not among them). */
const WORK: readonly Step[] = ["organisation", "app", "meter", "package", "event"];
const workIndex = computed(() => WORK.indexOf(step.value));

const app = computed(() => apps.data.value?.apps?.find((a) => a.id === progress.value?.app));
const tenantId = computed(() => tenantOf(app.value, "sandbox"));
const currency = computed(() => progress.value?.currency ?? app.value?.tenants?.find((x) => x.id === tenantId.value)?.currency ?? FALLBACK_CURRENCY);

// --- the fields, filled from what is stored so a returning person sees their own choices
const orgName = ref("");
const orgCurrency = ref<Currency>((progress.value?.currency as Currency | undefined) ?? FALLBACK_CURRENCY);
const orgZone = ref(progress.value?.timezone ?? ownZone());
const appName = ref("");
const chosen = ref<MeterTemplate["key"]>(templateByCode(progress.value?.meter)?.key ?? METER_TEMPLATES[0]!.key);
const pkgName = ref("");
const fee = ref("29");
const unitPrice = ref("");
const formError = ref("");

const isOwner = computed(() => account.data.value?.role === "owner");
watch(() => account.data.value?.name, (n) => { if (orgName.value === "") orgName.value = n ?? ""; }, { immediate: true });
const template = computed(() => METER_TEMPLATES.find((x) => x.key === chosen.value) ?? METER_TEMPLATES[0]!);
// The package step starts from the meter that was made: its price per unit is the template's.
watch(step, (s) => {
  formError.value = "";
  if (s === "app" && appName.value === "") appName.value = t("onboarding.app.defaultName");
  if (s === "package") {
    if (pkgName.value === "") pkgName.value = t("onboarding.package.defaultName");
    unitPrice.value = (templateByCode(progress.value?.meter) ?? template.value).unitPrice;
  }
}, { immediate: true });

const meters = useMeters(tenantId);
const createMeter = useCreateMeter(tenantId);
const createPackage = useCreatePackage(tenantId);
const sendEvent = useSendTestEvent(tenantId);
const busy = computed(() => renameAccount.isPending.value || createApp.isPending.value || createMeter.isPending.value || createPackage.isPending.value || sendEvent.isPending.value);

const heading = ref<HTMLElement | null>(null);
// A new step is announced by moving the focus to its heading.
watch(step, async () => {
  await nextTick();
  heading.value?.focus();
});

onMounted(() => heading.value?.focus());

const go = (to: Step) => patchProgress({ step: to });
const back = () => go(stepBy(step.value, -1));

function start() {
  patchProgress({ status: "active", step: "organisation" });
}

async function saveOrganisation() {
  formError.value = "";
  if (isOwner.value && orgName.value.trim() !== "" && orgName.value.trim() !== account.data.value?.name) {
    try {
      await renameAccount.mutateAsync(orgName.value.trim());
    } catch {
      return;
    }
  }
  patchProgress({ currency: orgCurrency.value, timezone: orgZone.value, step: "app" });
}

async function saveApp() {
  formError.value = "";
  // Coming back to this step after the app was made does not make another.
  if (app.value) return go("meter");
  try {
    const made = await createApp.mutateAsync({ name: appName.value.trim(), currency: orgCurrency.value, timezone: orgZone.value });
    patchProgress({ app: made.id, step: "meter" });
  } catch {
    // The error is on the mutation, under the form.
  }
}

async function saveMeter() {
  formError.value = "";
  const t0 = template.value;
  try {
    if (progress.value?.meter !== t0.meter.code) await createMeter.mutateAsync(t0.meter);
    patchProgress({ meter: t0.meter.code, step: "package" });
  } catch {
    // Shown under the form.
  }
}

async function savePackage() {
  formError.value = "";
  const meter = progress.value?.meter ?? template.value.meter.code;
  const base = parseMajor(fee.value, currency.value);
  if (base === null) {
    formError.value = t("billing.packages.badAmount", { currency: currency.value });
    return;
  }
  const usage = buildUsage([{ ...newPrice(meter), unitPrice: unitPrice.value }], [], meters.data.value ?? [], currency.value);
  if ("error" in usage) {
    formError.value = t(`billing.packages.usageErrors.${usage.error.key}`, { n: usage.error.row, meter: usage.error.meter, currency: currency.value });
    return;
  }
  const code = slug(pkgName.value);
  try {
    // A package of that code that exists already gets a new version, which is what a second try wants.
    await createPackage.mutateAsync({ code, name: pkgName.value.trim(), baseFeeMinor: base, fees: [], ...usage });
    patchProgress({ plan: code, step: "event" });
  } catch {
    // Shown under the form.
  }
}

async function send() {
  const t0 = templateByCode(progress.value?.meter) ?? template.value;
  try {
    await sendEvent.mutateAsync({
      plan: progress.value?.plan ?? "",
      meter: t0.meter.code,
      quantity: t0.sample,
      customerReady: progress.value?.customer === true,
      onCustomer: () => patchProgress({ customer: true }),
    });
    patchProgress({ event: true });
  } catch {
    // Shown under the button; a try again skips what the first made.
  }
}

const sentTemplate = computed(() => templateByCode(progress.value?.meter) ?? template.value);
const appId = computed(() => progress.value?.app ?? "");
const FLOW = [
  { key: "meter", icon: Gauge },
  { key: "price", icon: Tag },
  { key: "bill", icon: FileText },
  { key: "paid", icon: Wallet },
] as const;
/** Where the person goes from the last screen: the sandbox they just filled, then the things that come next. */
const NEXT = computed(() => [
  { key: "dashboard", to: `/app/apps/${appId.value}`, search: { env: "sandbox" } },
  { key: "keys", to: `/app/apps/${appId.value}/keys`, search: { env: "sandbox" } },
  { key: "team", to: "/app/members", search: {} },
  { key: "live", to: `/app/apps/${appId.value}/go-live`, search: { env: "live" } },
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
      <ol v-if="workIndex >= 0" class="flex items-center gap-2" :aria-label="t('onboarding.stepLabel', { n: workIndex + 1, total: WORK.length })" data-testid="onb-steps">
        <li v-for="(s, i) in WORK" :key="s" class="flex flex-1 flex-col gap-1.5" :aria-current="i === workIndex ? 'step' : undefined">
          <span :class="['h-1.5 rounded-full transition-colors', i <= workIndex ? 'bg-brand' : 'bg-border']"></span>
          <span :class="['hidden text-xs sm:block', i === workIndex ? 'text-foreground font-medium' : 'text-muted-foreground']">{{ t(`onboarding.steps.${s}`) }}</span>
        </li>
      </ol>

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

        <!-- Organisation -->
        <form v-else-if="step === 'organisation'" class="flex flex-col gap-5" @submit.prevent="saveOrganisation">
          <div class="flex flex-col gap-1.5">
            <h1 id="onb-title" ref="heading" tabindex="-1" class="text-2xl font-semibold tracking-tight outline-none">{{ t("onboarding.organisation.title") }}</h1>
            <p class="text-muted-foreground text-sm">{{ t("onboarding.organisation.body") }}</p>
          </div>
          <div v-if="isOwner" class="flex flex-col gap-1.5">
            <Label for="onb-org">{{ t("onboarding.organisation.name") }}</Label>
            <Input id="onb-org" v-model="orgName" autocomplete="organization" maxlength="200" required data-testid="onb-org-name" />
          </div>
          <div class="grid gap-4 sm:grid-cols-2">
            <div class="flex flex-col gap-1.5">
              <Label for="onb-currency">{{ t("onboarding.organisation.currency") }}</Label>
              <CurrencySelect id="onb-currency" v-model="orgCurrency" :aria-label="t('onboarding.organisation.currency')" />
            </div>
            <div class="flex flex-col gap-1.5">
              <Label for="onb-zone">{{ t("onboarding.organisation.timezone") }}</Label>
              <TimezoneSelect id="onb-zone" v-model="orgZone" :aria-label="t('onboarding.organisation.timezone')" />
            </div>
          </div>
          <p class="text-muted-foreground text-xs">{{ t("onboarding.organisation.note") }}</p>
          <ErrorText :error="renameAccount.error.value" />
          <div class="flex items-center justify-between gap-3">
            <Button type="button" variant="ghost" @click="back">{{ t("onboarding.back") }}</Button>
            <Button type="submit" :disabled="busy || (isOwner && orgName.trim() === '')" data-testid="onb-continue">{{ t("onboarding.continue") }}</Button>
          </div>
        </form>

        <!-- First app -->
        <form v-else-if="step === 'app'" class="flex flex-col gap-5" @submit.prevent="saveApp">
          <div class="flex flex-col gap-1.5">
            <h1 id="onb-title" ref="heading" tabindex="-1" class="text-2xl font-semibold tracking-tight outline-none">{{ t("onboarding.app.title") }}</h1>
            <p class="text-muted-foreground text-sm">{{ t("onboarding.app.body") }}</p>
          </div>
          <p v-if="app" class="bg-success-soft text-success flex items-center gap-2 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="onb-app-made">
            <Check class="size-4 shrink-0" aria-hidden="true" />{{ t("onboarding.app.created", { name: app.name }) }}
          </p>
          <div v-else class="flex flex-col gap-1.5">
            <Label for="onb-app">{{ t("onboarding.app.name") }}</Label>
            <Input id="onb-app" v-model="appName" autocomplete="off" maxlength="100" required data-testid="onb-app-name" />
          </div>
          <ErrorText :error="createApp.error.value" />
          <div class="flex items-center justify-between gap-3">
            <Button type="button" variant="ghost" :disabled="!!app" @click="back">{{ t("onboarding.back") }}</Button>
            <Button type="submit" :disabled="busy || (!app && appName.trim() === '')" data-testid="onb-continue">{{ app ? t("onboarding.continue") : t("onboarding.app.create") }}</Button>
          </div>
        </form>

        <!-- First meter, from a template -->
        <form v-else-if="step === 'meter'" class="flex flex-col gap-5" @submit.prevent="saveMeter">
          <div class="flex flex-col gap-1.5">
            <h1 id="onb-title" ref="heading" tabindex="-1" class="text-2xl font-semibold tracking-tight outline-none">{{ t("onboarding.meter.title") }}</h1>
            <p class="text-muted-foreground text-sm">{{ t("onboarding.meter.body") }}</p>
          </div>
          <div class="grid gap-3 sm:grid-cols-2" role="radiogroup" :aria-label="t('onboarding.meter.title')">
            <button
              v-for="m in METER_TEMPLATES"
              :key="m.key"
              type="button"
              role="radio"
              :aria-checked="chosen === m.key"
              :data-testid="`template-${m.key}`"
              :class="[
                'flex items-start gap-3 rounded-2xl border p-4 text-start transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
                chosen === m.key ? 'border-brand bg-brand-soft/50 ring-brand ring-1' : 'border-border hover:border-brand/60',
              ]"
              @click="chosen = m.key"
            >
              <span class="bg-brand-soft text-brand-strong flex size-10 shrink-0 items-center justify-center rounded-xl"><component :is="m.icon" class="size-5" aria-hidden="true" /></span>
              <span class="flex min-w-0 flex-col gap-0.5">
                <span class="font-semibold">{{ t(`onboarding.meter.templates.${m.key}.name`) }}</span>
                <span class="text-muted-foreground text-sm">{{ t(`onboarding.meter.templates.${m.key}.body`) }}</span>
                <span class="mono text-muted-foreground mt-1 text-xs">{{ m.meter.code }} · {{ m.meter.unit_label }}</span>
              </span>
            </button>
          </div>
          <ErrorText :error="createMeter.error.value" />
          <div class="flex items-center justify-between gap-3">
            <Button type="button" variant="ghost" @click="back">{{ t("onboarding.back") }}</Button>
            <Button type="submit" :disabled="busy || !tenantId" data-testid="onb-continue">{{ t("onboarding.meter.create") }}</Button>
          </div>
        </form>

        <!-- First package with a price -->
        <form v-else-if="step === 'package'" class="flex flex-col gap-5" @submit.prevent="savePackage">
          <div class="flex flex-col gap-1.5">
            <h1 id="onb-title" ref="heading" tabindex="-1" class="text-2xl font-semibold tracking-tight outline-none">{{ t("onboarding.package.title") }}</h1>
            <p class="text-muted-foreground text-sm">{{ t("onboarding.package.body") }}</p>
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="onb-pkg">{{ t("onboarding.package.name") }}</Label>
            <Input id="onb-pkg" v-model="pkgName" autocomplete="off" maxlength="100" required data-testid="onb-package-name" />
          </div>
          <div class="grid gap-4 sm:grid-cols-2">
            <div class="flex flex-col gap-1.5">
              <Label for="onb-fee">{{ t("onboarding.package.fee", { currency }) }}</Label>
              <Input id="onb-fee" v-model="fee" inputmode="decimal" required data-testid="onb-fee" />
            </div>
            <div class="flex flex-col gap-1.5">
              <Label for="onb-unit">{{ t("onboarding.package.unitPrice", { unit: sentTemplate.meter.unit_label, currency }) }}</Label>
              <Input id="onb-unit" v-model="unitPrice" inputmode="decimal" required data-testid="onb-unit-price" />
            </div>
          </div>
          <p class="text-muted-foreground text-xs">{{ t("onboarding.package.note") }}</p>
          <ErrorText :message="formError" />
          <ErrorText :error="createPackage.error.value" />
          <div class="flex items-center justify-between gap-3">
            <Button type="button" variant="ghost" @click="back">{{ t("onboarding.back") }}</Button>
            <Button type="submit" :disabled="busy || pkgName.trim() === ''" data-testid="onb-continue">{{ t("onboarding.package.create") }}</Button>
          </div>
        </form>

        <!-- A test event in the sandbox -->
        <div v-else-if="step === 'event'" class="flex flex-col gap-5">
          <div class="flex flex-col gap-1.5">
            <h1 id="onb-title" ref="heading" tabindex="-1" class="text-2xl font-semibold tracking-tight outline-none">{{ t("onboarding.event.title") }}</h1>
            <p class="text-muted-foreground text-sm">{{ t("onboarding.event.body") }}</p>
          </div>
          <dl class="bg-muted/60 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 rounded-2xl p-4 text-sm">
            <dt class="text-muted-foreground">{{ t("onboarding.event.customer") }}</dt>
            <dd class="mono m-0">{{ TEST_CUSTOMER }}</dd>
            <dt class="text-muted-foreground">{{ t("onboarding.event.meter") }}</dt>
            <dd class="mono m-0">{{ sentTemplate.meter.code }}</dd>
            <dt class="text-muted-foreground">{{ t("onboarding.event.quantity") }}</dt>
            <dd class="m-0">{{ sentTemplate.sample }} {{ sentTemplate.meter.unit_label }}</dd>
          </dl>
          <p v-if="progress?.event" class="bg-success-soft text-success flex items-center gap-2 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="onb-event-sent">
            <Check class="size-4 shrink-0" aria-hidden="true" />{{ t("onboarding.event.sent", { quantity: sentTemplate.sample, unit: sentTemplate.meter.unit_label }) }}
          </p>
          <ErrorText :error="sendEvent.error.value" />
          <div class="flex items-center justify-between gap-3">
            <Button type="button" variant="ghost" @click="back">{{ t("onboarding.back") }}</Button>
            <div class="flex items-center gap-2">
              <Button v-if="progress?.event" type="button" data-testid="onb-continue" @click="go('done')">{{ t("onboarding.continue") }}</Button>
              <Button v-else type="button" :disabled="busy" data-testid="onb-send" @click="send">{{ sendEvent.isPending.value ? t("onboarding.event.sending") : t("onboarding.event.send") }}</Button>
            </div>
          </div>
        </div>

        <!-- Done -->
        <template v-else>
          <div class="flex flex-col gap-1.5">
            <span class="bg-success-soft text-success flex size-12 items-center justify-center rounded-2xl"><Check class="size-6" aria-hidden="true" /></span>
            <h1 id="onb-title" ref="heading" tabindex="-1" class="mt-2 text-2xl font-semibold tracking-tight outline-none">{{ t("onboarding.done.title") }}</h1>
            <p class="text-muted-foreground text-sm">{{ t("onboarding.done.body") }}</p>
          </div>
          <ul class="flex flex-col gap-2" data-testid="onb-next">
            <li v-for="n in NEXT" :key="n.key">
              <Link :to="n.to" :search="n.search as never" class="hover:bg-muted flex items-center justify-between gap-3 rounded-2xl border px-4 py-3 text-sm" @click="finishOnboarding()">
                <span><span class="font-medium">{{ t(`onboarding.done.next.${n.key}.title`) }}</span><span class="text-muted-foreground block">{{ t(`onboarding.done.next.${n.key}.body`) }}</span></span>
                <ArrowRight class="size-4 shrink-0 rtl:rotate-180" aria-hidden="true" />
              </Link>
            </li>
          </ul>
          <div class="flex justify-end">
            <Button size="lg" data-testid="onb-finish" @click="finishOnboarding()">{{ t("onboarding.done.finish") }}</Button>
          </div>
        </template>
      </section>
</div>
  </div>
</template>
