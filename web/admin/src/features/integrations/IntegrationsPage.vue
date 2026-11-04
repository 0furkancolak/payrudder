<script setup lang="ts">
import { ExternalLink } from "@lucide/vue";
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref, type Ref } from "vue";
import { useI18n } from "vue-i18n";
import { fromTenant } from "@/features/product/billingProfile";
import { useAccount, useApp, useApps, useSandboxProviders } from "@/features/product/api";
import { readOf, resultOf, together } from "@/shared/lib/reads";
import { useSheetTarget } from "@/shared/lib/useSheetTarget";
import { LIVE_PROVIDERS, type LiveProviderName } from "@/features/product/liveProvider";
import { useAppsProviders, type ConnectEnvironment } from "@/features/providers/connectApi";
import { appRows, connectionOf, orgSummary, type ConnectionState, type Scope } from "@/features/providers/connections";
import ProviderSheet from "@/features/providers/ProviderSheet.vue";
import { Button } from "@/shared/ui/button";
import { ErrorState } from "@/shared/ui/error-state";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useEInvoiceIntegration, useMailIntegration, type EInvoiceProvider } from "./api";
import { BRANDS, CONNECTABLE, GROUPS, IN_GROUP, type IntegrationId } from "./brands";
import AccountsCard from "./AccountsCard.vue";
import AuditCard from "./AuditCard.vue";
import RoutingTester from "./RoutingTester.vue";
import { eInvoiceAccounts, mailAccounts } from "./accounts";
import EInvoiceSheet from "./EInvoiceSheet.vue";
import MailSheet from "./MailSheet.vue";
import OrganisationMailCard from "./OrganisationMailCard.vue";
import ProviderLogo from "./ProviderLogo.vue";
import { builtInMailState, eInvoiceState, ownMailState, stateTone, type CardState } from "./status";

/**
 * The integrations of an app, or of the whole organisation (no app in the address), in three groups: payments (iyzico, Paddle, bank transfer), e-invoice (the
 * integrators) and e-mail (Payrudder's own sender and the tenant's own provider). One card per integration: its mark, whether it is connected in live mode
 * and whether the other side accepts the credentials now. "Connect" opens a side sheet that asks for the credentials, checks them and can test the connection
 * or disconnect it. The sandbox needs no connection anywhere: it runs against built-in simulators and its mail goes to the sandbox inbox; the owner of an app
 * can still connect iyzico or Paddle in test mode, which a sandbox charge with the provider's own token then goes to. On the organisation screen a payment
 * provider can be connected on every app at once; the e-invoice and e-mail integrations (and the test mode) are set up per app.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const appId = computed(() => String((params.value as { appId?: string }).appId ?? ""));
const inApp = computed(() => appId.value !== "");

const apps = useApps();
const account = useAccount();
const app = useApp(appId);
const rows = computed(() => appRows(apps.data.value?.apps));
const overview = useAppsProviders(rows);
const sandboxAccounts = useSandboxProviders(appId, () => inApp.value);
const isOwner = computed(() => (inApp.value ? app.data.value?.role === "owner" : account.data.value?.role === "owner"));
// The reads the whole page stands on: who the owner is and which apps have a live environment. Without them a card can say nothing true (an owner would be
// told only an owner can connect), so the page shows the failure and no card.
const base = computed(() => together([readOf(apps), readOf(inApp.value ? app : account)]));
const loading = computed(() => base.value.loading);
const ready = computed(() => !base.value.loading && !base.value.error);
const liveApps = computed(() => rows.value.filter((r) => r.hasLive));
/** The organisation-wide choice is for an owner of an organisation with two or more apps that can take a live account. */
const canOrg = computed(() => isOwner.value && liveApps.value.length > 1);
const appName = computed(() => rows.value.find((r) => r.id === appId.value)?.name ?? "");
const hasLive = computed(() => (inApp.value ? (rows.value.find((r) => r.id === appId.value)?.hasLive ?? false) : liveApps.value.length > 0));
const live = computed(() => app.data.value?.app?.tenants?.find((x) => x.environment === "ENVIRONMENT_LIVE"));
const taxId = computed(() => fromTenant(live.value).taxId);

const eInvoice = useEInvoiceIntegration(appId, () => inApp.value && hasLive.value);
const eAccounts = computed(() => eInvoiceAccounts(eInvoice.data.value));
const mail = useMailIntegration(appId, () => inApp.value && hasLive.value);
// What each group of cards states its connections from. A group whose read failed shows the failure and not "Connect" next to a provider that is connected.
const reads = computed(() => {
  const live = overview.value.filter((r) => r.app.hasLive && r.query && (!inApp.value || r.app.id === appId.value));
  return {
    payments: together([...live.map((r) => resultOf(r.query!)), ...(inApp.value ? [readOf(sandboxAccounts)] : [])]),
    einvoice: together(inApp.value ? [readOf(eInvoice)] : []),
    email: together(inApp.value ? [readOf(mail)] : []),
  };
});

const names = (p: string) => t(`product.goLive.provider.names.${p}`);
const nameOf = (id: IntegrationId) => BRANDS[id].name ?? t(`product.integrations.names.${id}`);
const stateLabel = (s: CardState) => t(`product.integrations.state.${s}`);

/** What a card says: its state and the line under it. */
function card(id: IntegrationId): { state: CardState; line: string } {
  switch (id) {
    case "iyzico":
    case "paddle":
    case "stripe":
    case "banktransfer":
      return paymentLine(id);
    case "simulator":
      return { state: "always", line: t("product.integrations.lines.simulatorPayments") };
    case "einvoiceSimulator":
      return { state: "always", line: t("product.integrations.lines.simulatorEInvoice") };
    case "nilvera":
    case "webhook": {
      if (!inApp.value) return { state: "none", line: t("product.integrations.lines.perApp") };
      const s = eInvoiceState(eAccounts.value, id);
      const mode = t("product.integrations.einvoice.modeLine", { mode: t(`product.integrations.einvoice.modes.${s.mode || "production"}`) });
      return { state: s.state, line: s.state !== "working" ? t("product.providers.notConnectedLine") : s.count > 1 ? t("product.integrations.einvoice.accountsLine", { count: s.count }) : mode };
    }
    case "payrudderMail": {
      if (!inApp.value) return { state: "always", line: t("product.integrations.lines.mailBuiltIn") };
      const s = builtInMailState(mail.data.value);
      return { state: s, line: s === "unused" ? t("product.integrations.lines.mailUnused") : t("product.integrations.lines.mailBuiltIn") };
    }
    case "nafru": {
      if (!inApp.value) return { state: "none", line: t("product.integrations.lines.perApp") };
      const s = ownMailState(mail.data.value);
      const problem = mail.data.value?.integration?.check_problem;
      const n = mailAccounts(mail.data.value).length;
      return { state: s, line: s === "problem" && problem ? t(`product.integrations.testFailed.${problem}`) : s === "working" ? (n > 1 ? t("product.integrations.einvoice.accountsLine", { count: n }) : t("product.integrations.lines.mailOwn", { from: mail.data.value?.integration?.from ?? "" })) : t("product.providers.notConnectedLine") };
    }
  }
}

/** The providers with a test mode a person can connect in the sandbox: a bank transfer has none. */
const hasTestMode = (id: IntegrationId) => id === "iyzico" || id === "paddle" || id === "stripe";
/** The sandbox row of a card: the simulator, unless the app connected the provider's test mode. */
function sandboxRow(id: IntegrationId, g: string): { state: ConnectionState; label: string } {
  if (inApp.value && hasTestMode(id)) {
    const c = connectionOf(sandboxAccounts.data.value?.accounts, id);
    if (c.state === "working") return { state: "working", label: t("product.providers.sandbox.working") };
    if (c.state === "problem") return { state: "problem", label: t("product.providers.sandbox.problem") };
  }
  return { state: "none", label: t(`product.integrations.sandbox.${g}`) };
}

/** What a payment provider's card says about the live environment. */
function paymentLine(p: LiveProviderName): { state: CardState; line: string } {
  if (inApp.value) {
    const c = connectionOf(overview.value.find((r) => r.app.id === appId.value)?.accounts, p);
    const text = c.problem ? t(`product.goLive.provider.accountProblems.${c.problem}`) : c.state === "working" ? (c.isDefault ? t("product.providers.defaultAccount") : t("product.providers.connectedLine")) : t("product.providers.notConnectedLine");
    return { state: c.state, line: text };
  }
  const s = orgSummary(overview.value, p);
  const text = s.apps === 0 ? t("product.providers.noLiveApps") : s.connected === 0 ? t("product.providers.notConnectedLine") : t("product.providers.orgLine", { connected: s.connected, total: s.apps });
  return { state: s.state, line: text };
}

/** Whether a card has a button: the connectable ones, except e-invoice and e-mail on the organisation screen (those are set up per app). */
const hasButton = (id: IntegrationId) => (CONNECTABLE.includes(id) || id === "payrudderMail") && (inApp.value || BRANDS[id].group === "payments");

// --- payments: the existing sheet, for one app or for all
const sheetOpen = ref(false);
const sheetProvider = ref<LiveProviderName | "">("");
const sheetApp = ref("");
const sheetScope = ref<Scope>("app");
const sheetEnv = ref<ConnectEnvironment>("live");
const notice = ref("");

function openPayments(p: LiveProviderName, forApp: string, scope: Scope, environment: ConnectEnvironment = "live") {
  sheetProvider.value = p;
  sheetApp.value = forApp;
  sheetScope.value = scope;
  sheetEnv.value = environment;
  notice.value = "";
  sheetOpen.value = true;
}
/** From a card: on an app's screen the app, on the organisation's screen every app (an owner with several) or the only app. */
function fromPaymentCard(p: LiveProviderName) {
  if (inApp.value) return openPayments(p, appId.value, "app");
  const single = liveApps.value.length === 1 ? (liveApps.value[0]?.id ?? "") : "";
  openPayments(p, single, canOrg.value ? "org" : "app");
}
/** The test mode of a card, on an app's screen only: the sandbox is connected for one app. */
const fromSandboxCard = (p: LiveProviderName) => openPayments(p, appId.value, "app", "sandbox");
function onDone(s: { provider: LiveProviderName; scope: Scope; ok: number; failed: number }) {
  const key = sheetEnv.value === "sandbox" ? "product.providers.sandbox.done" : "product.providers.doneAll";
  notice.value = s.failed === 0 ? t(key, { provider: names(s.provider), count: s.ok }) : t("product.providers.donePartial", { provider: names(s.provider), ok: s.ok, failed: s.failed });
}

// --- e-invoice and e-mail
const eInvoiceOpen = ref(false);
const eInvoiceProvider = ref<EInvoiceProvider | "">("");
/** The key of the e-invoice account the sheet manages; empty when it adds one. */
const eInvoiceAccount = ref("");
const mailOpen = ref(false);
const mailKind = ref<"payrudderMail" | "nafru" | "">("");
/** The key of the e-mail account the sheet manages; empty when it adds one. */
const mailAccount = ref("");

/** The e-invoice sheet for an account of an integrator (its key), or for a new one (empty). */
function openEInvoice(provider: EInvoiceProvider, account: string) {
  eInvoiceProvider.value = provider;
  eInvoiceAccount.value = account;
  eInvoiceOpen.value = true;
}

function open(id: IntegrationId) {
  if ((LIVE_PROVIDERS as readonly string[]).includes(id)) return fromPaymentCard(id as LiveProviderName);
  if (id === "nilvera" || id === "webhook") openEInvoice(id, eAccounts.value.find((a) => a.provider === id)?.account ?? "");
  else if (id === "payrudderMail" || id === "nafru") openMail(id, mailAccounts(mail.data.value).find((a) => a.is_default)?.account ?? "");
}

/** The e-mail sheet for an account of the own provider (its key), or for a new one (empty). */
function openMail(kind: "payrudderMail" | "nafru", account: string) {
  mailKind.value = kind;
  mailAccount.value = account;
  mailOpen.value = true;
}

// What a sheet is open for is in the address (`connect-provider:stripe`), so a reload brings it back once the apps have loaded.
const named = <T extends string>(of: Ref<T | "">) => computed<T | null>({ get: () => of.value || null, set: (v) => (of.value = v ?? "") });
// A payment sheet is named `iyzico`, or `sandbox-iyzico` for the test mode of the sandbox.
const SANDBOX_PREFIX = "sandbox-";
const paymentSheet = computed<string | null>({
  get: () => (sheetProvider.value ? (sheetEnv.value === "sandbox" ? SANDBOX_PREFIX + sheetProvider.value : sheetProvider.value) : null),
  set: (v) => {
    sheetProvider.value = (v?.startsWith(SANDBOX_PREFIX) ? v.slice(SANDBOX_PREFIX.length) : (v ?? "")) as LiveProviderName | "";
    sheetEnv.value = v?.startsWith(SANDBOX_PREFIX) ? "sandbox" : "live";
  },
});
useSheetTarget("connect-provider", sheetOpen, paymentSheet, {
  id: (p) => p,
  find: (id) => {
    const sandbox = id.startsWith(SANDBOX_PREFIX);
    const p = sandbox ? id.slice(SANDBOX_PREFIX.length) : id;
    return ready.value && (LIVE_PROVIDERS as readonly string[]).includes(p) && (!sandbox || (inApp.value && hasTestMode(p as LiveProviderName))) ? id : undefined;
  },
  restore: (id) => (id.startsWith(SANDBOX_PREFIX) ? fromSandboxCard(id.slice(SANDBOX_PREFIX.length) as LiveProviderName) : fromPaymentCard(id as LiveProviderName)),
});
useSheetTarget("integration-einvoice", eInvoiceOpen, named(eInvoiceProvider), { id: (p) => p, find: (id) => (ready.value && (id === "nilvera" || id === "webhook") ? id : undefined), restore: open });
useSheetTarget("integration-mail", mailOpen, named(mailKind), { id: (k) => k, find: (id) => (ready.value && (id === "payrudderMail" || id === "nafru") ? id : undefined), restore: open });

const buttonLabel = (id: IntegrationId) => {
  if (id === "payrudderMail") return t("product.integrations.settings");
  return card(id).state === "none" ? t("product.providers.connect") : t("product.providers.manage");
};
/** The built-in sender's switches are for any member to see; the others need an owner. */
const disabled = (id: IntegrationId) => (id === "payrudderMail" ? false : !isOwner.value);
</script>

<template>
  <section class="flex flex-col gap-6" data-testid="integrations-screen">
    <header class="px-1">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("shell.items.integrations") }}</h1>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ inApp ? t("product.integrations.introApp", { app: appName }) : t("product.integrations.introOrg") }}</p>
    </header>

    <Skeleton v-if="loading" class="h-48 rounded-2xl" />
    <ErrorState v-else-if="base.error" :error="base.error" :retrying="base.fetching" compact class="mx-0" @retry="base.retry()" />
    <p v-else-if="!hasLive" class="text-muted-foreground surface p-6 text-sm" data-testid="integrations-no-live">{{ t("product.goLive.noLive") }}</p>
    <template v-else>
      <p v-if="notice" class="text-success text-sm" role="status" data-testid="provider-connected">{{ notice }}</p>
      <p v-if="!isOwner" class="text-muted-foreground text-sm" data-testid="integrations-owner-only">{{ t("product.goLive.provider.ownerOnly") }}</p>

      <section v-for="g in GROUPS" :key="g" class="flex flex-col gap-3" :data-testid="`integration-group-${g}`" :aria-labelledby="`group-${g}`">
        <div class="px-1">
          <h2 :id="`group-${g}`" class="text-lg font-semibold tracking-tight">{{ t(`product.integrations.groups.${g}.title`) }}</h2>
          <p class="text-muted-foreground text-sm">{{ t(`product.integrations.groups.${g}.intro`) }}</p>
        </div>
        <Skeleton v-if="reads[g].loading" class="h-40 rounded-2xl" />
        <ErrorState v-else-if="reads[g].error" :error="reads[g].error" :retrying="reads[g].fetching" compact class="mx-0" @retry="reads[g].retry()" />
        <ul v-else class="grid gap-4 lg:grid-cols-3" :aria-label="t(`product.integrations.groups.${g}.title`)">
          <li v-for="id in IN_GROUP[g]" :key="id" class="surface flex flex-col gap-4 p-5" :data-testid="`integration-card-${id}`" :data-state="card(id).state">
            <div class="flex items-center gap-3">
              <ProviderLogo :id="id" />
              <div class="min-w-0">
                <h3 class="text-base font-semibold tracking-tight" :data-testid="`integration-name-${id}`">{{ nameOf(id) }}</h3>
                <p class="text-muted-foreground text-xs">{{ t(`product.integrations.about.${id}`) }}</p>
              </div>
            </div>
            <div class="grid gap-2 text-sm">
              <div class="flex items-center justify-between gap-3">
                <span class="text-muted-foreground">{{ t("product.env.live") }}</span>
                <span><StatusPill :tone="stateTone(card(id).state)" :data-testid="`integration-state-${id}`">{{ stateLabel(card(id).state) }}</StatusPill></span>
              </div>
              <p class="text-muted-foreground -mt-1 text-xs" :class="card(id).state === 'problem' ? 'text-destructive' : ''">{{ card(id).line }}</p>
              <div class="flex items-center justify-between gap-3">
                <span class="text-muted-foreground">{{ t("product.env.sandbox") }}</span>
                <span><StatusPill :tone="stateTone(sandboxRow(id, g).state === 'none' ? 'always' : sandboxRow(id, g).state)" :data-testid="`integration-sandbox-${id}`">{{ sandboxRow(id, g).label }}</StatusPill></span>
              </div>
            </div>
            <div class="mt-auto flex flex-wrap items-center gap-2">
              <Button v-if="hasButton(id)" :variant="card(id).state === 'none' ? 'default' : 'outline'" :disabled="disabled(id)" :data-testid="`integration-open-${id}`" @click="open(id)">{{ buttonLabel(id) }}</Button>
              <Button v-else-if="id === 'payrudderMail' && !inApp" variant="outline" disabled>{{ t("product.integrations.settings") }}</Button>
              <Button v-if="inApp && hasTestMode(id)" variant="outline" :disabled="!isOwner" :data-testid="`integration-open-sandbox-${id}`" @click="fromSandboxCard(id as LiveProviderName)">
                {{ sandboxRow(id, g).state === "none" ? t("product.providers.sandbox.connect") : t("product.providers.sandbox.manage") }}
              </Button>
              <a v-if="BRANDS[id].url && CONNECTABLE.includes(id)" :href="BRANDS[id].url" target="_blank" rel="noopener noreferrer" class="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 text-xs hover:underline" :data-testid="`integration-link-${id}`">
                {{ t("product.integrations.website") }}<ExternalLink class="size-3" aria-hidden="true" />
              </a>
            </div>
          </li>
        </ul>
        <p v-if="g === 'payments'" class="text-muted-foreground px-1 text-xs">{{ t("product.providers.sandboxNote") }}</p>
        <p v-if="!inApp" class="text-muted-foreground px-1 text-xs" :data-testid="`integration-per-app-${g}`">{{ t("product.integrations.perAppNote") }}</p>
      </section>

      <template v-if="inApp">
        <AccountsCard :app-id="appId" :apps="rows" :is-owner="isOwner" @einvoice="openEInvoice" @mail="openMail" />
        <RoutingTester :app-id="appId" />
        <AuditCard :app-id="appId" />
      </template>

      <OrganisationMailCard v-if="!inApp" :apps="rows" />

      <div v-if="!inApp && rows.length > 0" class="surface flex flex-col gap-3 p-5" data-testid="integrations-apps">
        <div>
          <h2 class="text-base font-semibold tracking-tight">{{ t("product.providers.appsTitle") }}</h2>
          <p class="text-muted-foreground text-sm">{{ t("product.integrations.appsIntro") }}</p>
        </div>
        <div class="relative overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="text-muted-foreground border-b text-start text-xs">
                <th class="py-2 pe-3 text-start font-medium">{{ t("product.providers.app") }}</th>
                <th v-for="p in LIVE_PROVIDERS" :key="p" class="px-3 py-2 text-start font-medium">{{ names(p) }}</th>
                <th class="px-3 py-2 text-start font-medium"><span class="sr-only">{{ t("product.integrations.open") }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in overview" :key="r.app.id" class="border-b last:border-0" :data-testid="`integrations-app-${r.app.id}`">
                <th scope="row" class="py-2.5 pe-3 text-start font-medium">{{ r.app.name }}</th>
                <td v-for="p in LIVE_PROVIDERS" :key="p" class="px-3 py-2.5">
                  <span v-if="!r.app.hasLive" class="text-muted-foreground text-xs">{{ t("product.providers.noLive") }}</span>
                  <span v-else-if="r.query?.error" class="text-destructive text-xs" :data-testid="`integrations-unread-${r.app.id}-${p}`">{{ t("product.integrations.unread") }}</span>
                  <button v-else type="button" class="rounded-full focus-visible:ring-ring/50 focus-visible:ring-[3px] focus-visible:outline-none disabled:cursor-default" :disabled="!isOwner" :data-testid="`integrations-cell-${r.app.id}-${p}`" :aria-label="`${r.app.name}: ${names(p)}`" @click="openPayments(p, r.app.id, 'app')">
                    <StatusPill :tone="stateTone(connectionOf(r.accounts, p).state)">{{ stateLabel(connectionOf(r.accounts, p).state) }}</StatusPill>
                  </button>
                </td>
                <td class="px-3 py-2.5">
                  <Link :to="`/app/apps/${r.app.id}/integrations`" :search="{ env: 'live' } as never" class="text-primary text-xs font-medium hover:underline" :data-testid="`integrations-open-app-${r.app.id}`">{{ t("product.integrations.open") }}</Link>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>

    <ProviderSheet v-model:open="sheetOpen" :provider="sheetProvider" :app-id="sheetApp" :apps="rows" :scope="sheetScope" :can-org="canOrg" :is-owner="isOwner" :environment="sheetEnv" @done="onDone" />
    <EInvoiceSheet v-model:open="eInvoiceOpen" :provider="eInvoiceProvider" :app-id="appId" :account="eInvoiceAccount" :accounts="eAccounts" :is-owner="isOwner" :tax-id="taxId" />
    <MailSheet v-model:open="mailOpen" :kind="mailKind" :app-id="appId" :is-owner="isOwner" :account="mailAccount" />
  </section>
</template>
