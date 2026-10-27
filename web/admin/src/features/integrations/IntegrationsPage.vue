<script setup lang="ts">
import { ExternalLink } from "@lucide/vue";
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { fromTenant } from "@/features/product/billingProfile";
import { useAccount, useApp, useApps } from "@/features/product/api";
import { LIVE_PROVIDERS, type LiveProviderName } from "@/features/product/liveProvider";
import { useAppsProviders } from "@/features/providers/connectApi";
import { appRows, connectionOf, orgSummary, type Scope } from "@/features/providers/connections";
import ProviderSheet from "@/features/providers/ProviderSheet.vue";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useEInvoiceIntegration, useMailIntegration, type EInvoiceProvider } from "./api";
import { BRANDS, CONNECTABLE, GROUPS, IN_GROUP, type IntegrationId } from "./brands";
import EInvoiceSheet from "./EInvoiceSheet.vue";
import MailSheet from "./MailSheet.vue";
import ProviderLogo from "./ProviderLogo.vue";
import { builtInMailState, eInvoiceState, ownMailState, stateTone, type CardState } from "./status";

/**
 * The integrations of an app, or of the whole organisation (no app in the address), in three groups: payments (iyzico, Paddle, bank transfer), e-invoice (the
 * integrators) and e-mail (Payrudder's own sender and the tenant's own provider). One card per integration: its mark, whether it is connected in live mode
 * and whether the other side accepts the credentials now. "Connect" opens a side sheet that asks for the credentials, checks them and can test the connection
 * or disconnect it. The sandbox needs no connection anywhere: it runs against built-in simulators and its mail goes to the sandbox inbox. On the organisation
 * screen a payment provider can be connected on every app at once; the e-invoice and e-mail integrations are set up per app.
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
const isOwner = computed(() => (inApp.value ? app.data.value?.role === "owner" : account.data.value?.role === "owner"));
const loading = computed(() => apps.isPending.value || (inApp.value && app.isPending.value));
const liveApps = computed(() => rows.value.filter((r) => r.hasLive));
/** The organisation-wide choice is for an owner of an organisation with two or more apps that can take a live account. */
const canOrg = computed(() => isOwner.value && liveApps.value.length > 1);
const appName = computed(() => rows.value.find((r) => r.id === appId.value)?.name ?? "");
const hasLive = computed(() => (inApp.value ? (rows.value.find((r) => r.id === appId.value)?.hasLive ?? false) : liveApps.value.length > 0));
const live = computed(() => app.data.value?.app?.tenants?.find((x) => x.environment === "ENVIRONMENT_LIVE"));
const taxId = computed(() => fromTenant(live.value).taxId);

const eInvoice = useEInvoiceIntegration(appId, () => inApp.value && hasLive.value);
const mail = useMailIntegration(appId, () => inApp.value && hasLive.value);

const names = (p: string) => t(`product.goLive.provider.names.${p}`);
const nameOf = (id: IntegrationId) => BRANDS[id].name ?? t(`product.integrations.names.${id}`);
const stateLabel = (s: CardState) => t(`product.integrations.state.${s}`);

/** What a card says: its state and the line under it. */
function card(id: IntegrationId): { state: CardState; line: string } {
  switch (id) {
    case "iyzico":
    case "paddle":
    case "banktransfer":
      return paymentLine(id);
    case "simulator":
      return { state: "always", line: t("product.integrations.lines.simulatorPayments") };
    case "einvoiceSimulator":
      return { state: "always", line: t("product.integrations.lines.simulatorEInvoice") };
    case "nilvera":
    case "webhook": {
      if (!inApp.value) return { state: "none", line: t("product.integrations.lines.perApp") };
      const s = eInvoiceState(eInvoice.data.value, id);
      return { state: s.state, line: s.state === "working" ? t("product.integrations.einvoice.modeLine", { mode: t(`product.integrations.einvoice.modes.${s.mode || "production"}`) }) : t("product.providers.notConnectedLine") };
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
      return { state: s, line: s === "problem" && problem ? t(`product.integrations.testFailed.${problem}`) : s === "working" ? t("product.integrations.lines.mailOwn", { from: mail.data.value?.integration?.from ?? "" }) : t("product.providers.notConnectedLine") };
    }
  }
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
const notice = ref("");

function openPayments(p: LiveProviderName, forApp: string, scope: Scope) {
  sheetProvider.value = p;
  sheetApp.value = forApp;
  sheetScope.value = scope;
  notice.value = "";
  sheetOpen.value = true;
}
/** From a card: on an app's screen the app, on the organisation's screen every app (an owner with several) or the only app. */
function fromPaymentCard(p: LiveProviderName) {
  if (inApp.value) return openPayments(p, appId.value, "app");
  const single = liveApps.value.length === 1 ? (liveApps.value[0]?.id ?? "") : "";
  openPayments(p, single, canOrg.value ? "org" : "app");
}
function onDone(s: { provider: LiveProviderName; scope: Scope; ok: number; failed: number }) {
  notice.value = s.failed === 0 ? t("product.providers.doneAll", { provider: names(s.provider), count: s.ok }) : t("product.providers.donePartial", { provider: names(s.provider), ok: s.ok, failed: s.failed });
}

// --- e-invoice and e-mail
const eInvoiceOpen = ref(false);
const eInvoiceProvider = ref<EInvoiceProvider | "">("");
const mailOpen = ref(false);
const mailKind = ref<"payrudderMail" | "nafru" | "">("");

function open(id: IntegrationId) {
  if ((LIVE_PROVIDERS as readonly string[]).includes(id)) return fromPaymentCard(id as LiveProviderName);
  if (id === "nilvera" || id === "webhook") {
    eInvoiceProvider.value = id;
    eInvoiceOpen.value = true;
  } else if (id === "payrudderMail" || id === "nafru") {
    mailKind.value = id;
    mailOpen.value = true;
  }
}

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
    <ErrorText :error="apps.error.value" />
    <p v-if="!loading && !hasLive" class="text-muted-foreground surface p-6 text-sm" data-testid="integrations-no-live">{{ t("product.goLive.noLive") }}</p>
    <template v-else-if="!loading">
      <p v-if="notice" class="text-success text-sm" role="status" data-testid="provider-connected">{{ notice }}</p>
      <p v-if="!isOwner" class="text-muted-foreground text-sm" data-testid="integrations-owner-only">{{ t("product.goLive.provider.ownerOnly") }}</p>

      <section v-for="g in GROUPS" :key="g" class="flex flex-col gap-3" :data-testid="`integration-group-${g}`" :aria-labelledby="`group-${g}`">
        <div class="px-1">
          <h2 :id="`group-${g}`" class="text-lg font-semibold tracking-tight">{{ t(`product.integrations.groups.${g}.title`) }}</h2>
          <p class="text-muted-foreground text-sm">{{ t(`product.integrations.groups.${g}.intro`) }}</p>
        </div>
        <ul class="grid gap-4 lg:grid-cols-3" :aria-label="t(`product.integrations.groups.${g}.title`)">
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
                <span><StatusPill tone="info">{{ t(`product.integrations.sandbox.${g}`) }}</StatusPill></span>
              </div>
            </div>
            <div class="mt-auto flex flex-wrap items-center gap-2">
              <Button v-if="hasButton(id)" :variant="card(id).state === 'none' ? 'default' : 'outline'" :disabled="disabled(id)" :data-testid="`integration-open-${id}`" @click="open(id)">{{ buttonLabel(id) }}</Button>
              <Button v-else-if="id === 'payrudderMail' && !inApp" variant="outline" disabled>{{ t("product.integrations.settings") }}</Button>
              <a v-if="BRANDS[id].url && CONNECTABLE.includes(id)" :href="BRANDS[id].url" target="_blank" rel="noopener noreferrer" class="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 text-xs hover:underline" :data-testid="`integration-link-${id}`">
                {{ t("product.integrations.website") }}<ExternalLink class="size-3" aria-hidden="true" />
              </a>
            </div>
          </li>
        </ul>
        <p v-if="g === 'payments'" class="text-muted-foreground px-1 text-xs">{{ t("product.providers.sandboxNote") }}</p>
        <p v-else-if="!inApp" class="text-muted-foreground px-1 text-xs" :data-testid="`integration-per-app-${g}`">{{ t("product.integrations.perAppNote") }}</p>
      </section>

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

    <ProviderSheet v-model:open="sheetOpen" :provider="sheetProvider" :app-id="sheetApp" :apps="rows" :scope="sheetScope" :can-org="canOrg" :is-owner="isOwner" @done="onDone" />
    <EInvoiceSheet v-model:open="eInvoiceOpen" :provider="eInvoiceProvider" :app-id="appId" :account="eInvoice.data.value" :is-owner="isOwner" :tax-id="taxId" />
    <MailSheet v-model:open="mailOpen" :kind="mailKind" :app-id="appId" :is-owner="isOwner" />
  </section>
</template>
