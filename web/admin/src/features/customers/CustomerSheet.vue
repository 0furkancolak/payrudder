<script setup lang="ts">
import { Ban, Check, CircleCheck, Copy, CreditCard, Coins, Link2, MapPin, Pencil, Plus, Undo2, UserPen } from "@lucide/vue";
import { TabsContent } from "reka-ui";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { CustomerAgreements } from "@/features/contracts";
import { useCreditBalance } from "@/features/credits/api";
import GrantCreditsSheet from "@/features/credits/GrantCreditsSheet.vue";
import { formatCredits } from "@/features/credits/lib";
import LedgerCard from "@/features/credits/LedgerCard.vue";
import { CustomerRelated, InvoiceRows, PaymentRows, SubscriptionRows, useCustomerInvoices } from "@/features/related";
import { useTenantCurrency } from "@/features/tenants/currency";
import { useCanWrite } from "@/shared/lib/access";
import { copyText } from "@/shared/lib/clipboard";
import { useFormat } from "@/shared/lib/format";
import { formatMinor, maskEmail } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { ErrorState } from "@/shared/ui/error-state";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/shared/ui/dropdown-menu";
import { Skeleton } from "@/shared/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import { addressLine, useCustomerBlockEvents } from "./api";
import CustomerAvatar from "./CustomerAvatar.vue";
import CorrectUsageSheet from "./CorrectUsageSheet.vue";
import CustomerPaymentPage from "./CustomerPaymentPage.vue";
import UpcomingInvoiceCard from "./UpcomingInvoiceCard.vue";
import { countryName, flagOf, headline, isBlocked, type Customer, type CustomerSummary, type CustomerTab } from "./lib";

/**
 * The body of a customer's sheet, after Stripe's customer page: who they are (avatar, e-mail, id to copy, country, since when), what can be done for
 * them, four figures (spend, what is open, credits, subscriptions that bill now) and a tab per kind of record. The tab is a
 * model, so a row action of the list can open the sheet on them. The figures come from the list page (`summary`), so list and sheet agree.
 */

/** `readonly`: the sheet opened over another one (a mention of the customer): it shows, it does not offer the forms that live on the customers page. */
const props = defineProps<{ tenantId: string; customer: Customer; summary?: CustomerSummary; summaryLoading?: boolean; readonly?: boolean }>();
const emit = defineEmits<{ editContact: []; editAddress: []; editTax: []; block: []; unblock: []; newSubscription: []; paymentLink: []; credits: [] }>();
const tab = defineModel<CustomerTab>("tab", { default: "overview" });
const canWrite = useCanWrite();
/** Nothing is offered that changes the customer: in a peek, and to a viewer, whom the server would refuse. */
const locked = computed(() => props.readonly || !canWrite.value);
const { t, locale } = useI18n();
const fmt = useFormat();

const id = computed(() => props.customer.external_id ?? "");
const balance = useCreditBalance(() => props.tenantId, id);
const invoices = useCustomerInvoices(() => props.tenantId, id);
const invoiceIds = computed(() => (invoices.data.value?.invoices ?? []).map((i) => i.id ?? ""));

const TABS: CustomerTab[] = ["overview", "payments", "invoices", "subscriptions", "credits", "agreements", "details"];
const spend = computed(() => (props.summary ? headline(props.summary.spend, props.customer.billing_currency) : undefined));
const open = computed(() => (props.summary ? headline(props.summary.open, props.customer.billing_currency) : undefined));
const tenantCurrency = useTenantCurrency(() => props.tenantId);
/** A zero still carries a currency: the customer's, else the one of an amount they have, else the tenant's. */
const zero = computed(() => formatMinor(0, props.customer.billing_currency || spend.value?.currency || open.value?.currency || tenantCurrency.value, locale.value) || "0");
const money = (h?: { minor: string; currency: string }) => (h ? formatMinor(h.minor, h.currency, locale.value) : zero.value);

// The block: whether it holds, and the history of blocks and lifts (read when the details are on screen).
const blocked = computed(() => isBlocked(props.customer));
const blockEvents = useCustomerBlockEvents(() => props.tenantId, () => (tab.value === "details" ? id.value : ""));
const categoryText = (c?: string) => (c ? t(`billing.customers.block.categories.${c}`) : "");

const granting = ref(false);
const correcting = ref(false);
const copied = ref(false);
async function copyId() {
  copied.value = await copyText(id.value);
  if (copied.value) setTimeout(() => (copied.value = false), 1500);
}
const country = computed(() => countryName(props.customer.country, locale.value));
const address = computed(() => addressLine(props.customer.address));
const tax = computed(() => [props.customer.tax_id, props.customer.tax_office].filter(Boolean).join(" · "));
</script>

<template>
  <div class="flex min-w-0 flex-col gap-6" data-testid="customer-detail">
    <!-- Who -->
    <div class="flex min-w-0 items-start gap-4">
      <CustomerAvatar :customer="customer" size="lg" />
      <div class="flex min-w-0 flex-1 flex-col gap-1.5">
        <span v-if="customer.email" class="truncate text-sm font-medium" dir="auto" data-testid="customer-email">{{ maskEmail(customer.email) }}</span>
        <div class="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs">
          <button
            type="button"
            class="bg-muted hover:bg-accent text-foreground inline-flex max-w-full items-center gap-1.5 rounded-lg px-2 py-1"
            :aria-label="t('billing.customers.sheet.copyId', { id })"
            data-testid="customer-copy-id"
            @click="copyId"
          >
            <span class="mono truncate">{{ id }}</span>
            <Check v-if="copied" class="text-success size-3.5" aria-hidden="true" />
            <Copy v-else class="size-3.5" aria-hidden="true" />
          </button>
          <span v-if="copied" role="status" class="sr-only">{{ t("common.copied") }}</span>
          <span v-if="customer.country" class="inline-flex items-center gap-1"><span aria-hidden="true">{{ flagOf(customer.country) }}</span>{{ country }}</span>
          <span v-if="customer.create_time">{{ t("billing.customers.sheet.since", { date: fmt.date(customer.create_time, "day") }) }}</span>
        </div>
      </div>
    </div>

    <!-- Blocked: why, until when, and the way out. A block that ran out by itself is said so until somebody clears it. -->
    <div
      v-if="customer.block_time"
      :class="['flex flex-wrap items-start justify-between gap-3 rounded-2xl px-4 py-3 text-sm', blocked ? 'bg-destructive-soft text-destructive' : 'bg-muted text-muted-foreground']"
      role="status"
      data-testid="customer-block-banner"
    >
      <div class="flex min-w-0 flex-col gap-0.5">
        <span class="inline-flex items-center gap-2 font-medium"><Ban class="size-4 shrink-0" aria-hidden="true" />{{ blocked ? t("billing.customers.block.banner", { date: fmt.date(customer.block_time, "day") }) : t("billing.customers.block.ended", { date: fmt.date(customer.blocked_until, "day") }) }}</span>
        <span v-if="blocked && customer.blocked_until" class="text-xs">{{ t("billing.customers.block.bannerUntil", { date: fmt.date(customer.blocked_until, "day") }) }}</span>
        <span v-if="blocked && customer.service_suspended" class="text-xs font-medium" data-testid="customer-suspended">{{ t("billing.customers.block.bannerSuspended") }}</span>
        <span v-if="customer.block_category || customer.block_reason" class="text-xs break-words" dir="auto">{{ [categoryText(customer.block_category), customer.block_reason].filter(Boolean).join(" · ") }}</span>
      </div>
      <Button v-if="!locked" size="sm" variant="outline" data-testid="customer-unblock" @click="emit('unblock')"><CircleCheck aria-hidden="true" />{{ t("billing.customers.block.lift") }}</Button>
    </div>

    <!-- What can be done -->
    <div v-if="!locked" class="flex flex-wrap gap-2" role="group" :aria-label="t('billing.customers.sheet.actions')">
      <Button size="sm" data-testid="customer-new-subscription" @click="emit('newSubscription')"><Plus aria-hidden="true" />{{ t("billing.customers.sheet.newSubscription") }}</Button>
      <Button size="sm" variant="outline" @click="emit('paymentLink')"><Link2 aria-hidden="true" />{{ t("billing.paymentLinks.paymentLink") }}</Button>
      <CustomerPaymentPage :tenant-id="tenantId" :customer="id" />
      <Button size="sm" variant="outline" data-testid="customer-correct-usage" @click="correcting = true"><Undo2 aria-hidden="true" />{{ t("billing.corrections.action") }}</Button>
      <Button v-if="!blocked" size="sm" variant="outline" data-testid="customer-block" @click="emit('block')"><Ban aria-hidden="true" />{{ t("billing.customers.block.action") }}</Button>
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button size="sm" variant="outline" data-testid="customer-edit"><Pencil aria-hidden="true" />{{ t("billing.customers.sheet.edit") }}</Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" class="min-w-44">
          <DropdownMenuItem class="rounded-xl" data-testid="customer-edit-contact" @select="emit('editContact')"><UserPen class="size-4" aria-hidden="true" />{{ t("billing.customers.contact.edit") }}</DropdownMenuItem>
          <DropdownMenuItem class="rounded-xl" data-testid="customer-edit-address" @select="emit('editAddress')"><MapPin class="size-4" aria-hidden="true" />{{ t("billing.customers.address.edit") }}</DropdownMenuItem>
          <DropdownMenuItem class="rounded-xl" data-testid="customer-edit-tax" @select="emit('editTax')"><CreditCard class="size-4" aria-hidden="true" />{{ t("einvoice.customer.edit") }}</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>

    <!-- Four figures -->
    <dl class="border-border/70 grid grid-cols-2 overflow-hidden rounded-2xl border sm:grid-cols-4" data-testid="customer-summary">
      <div class="border-border/70 flex min-w-0 flex-col gap-1 border-e border-b p-3 sm:border-b-0">
        <dt class="text-muted-foreground text-xs">{{ t("billing.customers.stats.spend") }}</dt>
        <dd class="m-0 truncate font-semibold tabular-nums" data-testid="customer-spend">
          <Skeleton v-if="summaryLoading" class="h-5 w-20" /><template v-else>{{ money(spend) }}<span v-if="spend?.more" class="text-muted-foreground ms-1 text-xs font-normal">+{{ spend.more }}</span></template>
        </dd>
      </div>
      <div class="border-border/70 flex min-w-0 flex-col gap-1 border-b p-3 sm:border-e sm:border-b-0">
        <dt class="text-muted-foreground text-xs">{{ t("billing.customers.stats.open") }}</dt>
        <dd class="m-0 truncate font-semibold tabular-nums" :class="open ? 'text-warning' : ''" data-testid="customer-open">
          <Skeleton v-if="summaryLoading" class="h-5 w-20" /><template v-else>{{ money(open) }}</template>
        </dd>
        <dd v-if="!summaryLoading && summary?.openInvoices" class="text-muted-foreground m-0 truncate text-xs">{{ t("billing.customers.stats.openInvoices", { count: summary.openInvoices }) }}</dd>
      </div>
      <div class="border-border/70 flex min-w-0 flex-col gap-1 border-e p-3">
        <dt class="text-muted-foreground text-xs">{{ t("billing.customers.stats.credits") }}</dt>
        <dd class="m-0 truncate font-semibold tabular-nums" data-testid="customer-credits">
          <Skeleton v-if="balance.isPending.value" class="h-5 w-16" /><template v-else>{{ balance.data.value ? formatCredits(balance.data.value.balance, locale) : "—" }}</template>
        </dd>
      </div>
      <div class="flex min-w-0 flex-col gap-1 p-3">
        <dt class="text-muted-foreground text-xs">{{ t("billing.customers.stats.subscriptions") }}</dt>
        <dd class="m-0 truncate font-semibold tabular-nums" data-testid="customer-active-subscriptions">
          <Skeleton v-if="summaryLoading" class="h-5 w-8" /><template v-else>{{ fmt.count(summary?.activeSubscriptions ?? 0) }}</template>
        </dd>
      </div>
    </dl>

    <CorrectUsageSheet v-if="!locked" v-model:open="correcting" :tenant-id="tenantId" :customer="id" />

    <Tabs v-model="tab" class="min-w-0 gap-4">
      <TabsList class="border-border/70 w-full flex-nowrap justify-start gap-4 overflow-x-auto border-b" :aria-label="t('billing.customers.sheet.sections')">
        <TabsTrigger
          v-for="x in TABS"
          :key="x"
          :value="x"
          class="text-muted-foreground hover:text-foreground data-[state=active]:text-foreground data-[state=active]:border-primary h-9 flex-none rounded-none border-0 border-b-2 border-transparent bg-transparent px-0.5 font-medium data-[state=active]:bg-transparent data-[state=active]:shadow-none"
          :data-testid="`customer-tab-${x}`"
          >
{{ t(`billing.customers.tabs.${x}`) }}
</TabsTrigger
        >
      </TabsList>

      <TabsContent value="overview" class="flex flex-col gap-6 outline-none">
        <UpcomingInvoiceCard :tenant-id="tenantId" :external-id="id" />
        <CustomerRelated :key="id" :tenant-id="tenantId" :external-id="id" :limit="3" @more="tab = $event" />
      </TabsContent>
      <TabsContent value="payments" class="outline-none">
        <PaymentRows :tenant-id="tenantId" :invoice-ids="invoiceIds" bare :empty-text="t('billing.customers.sheet.noPayments')" />
      </TabsContent>
      <TabsContent value="invoices" class="outline-none">
        <InvoiceRows :tenant-id="tenantId" :customer="id" bare :empty-text="t('billing.customers.sheet.noInvoices')" />
      </TabsContent>
      <TabsContent value="subscriptions" class="flex flex-col gap-3 outline-none">
        <SubscriptionRows :tenant-id="tenantId" :customer="id" bare :empty-text="t('billing.customers.sheet.noSubscriptions')" />
        <Button v-if="!locked" size="sm" variant="outline" class="self-start" @click="emit('newSubscription')"><Plus aria-hidden="true" />{{ t("billing.customers.sheet.newSubscription") }}</Button>
      </TabsContent>
      <TabsContent value="credits" class="flex flex-col gap-4 outline-none">
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div class="flex flex-col">
            <span class="text-muted-foreground text-xs">{{ t("billing.customers.stats.credits") }}</span>
            <span class="text-2xl font-semibold tabular-nums">{{ balance.data.value ? formatCredits(balance.data.value.balance, locale) : "—" }}</span>
            <span v-if="balance.data.value?.next_expire_time" class="text-muted-foreground text-xs">{{ t("billing.credits.customer.nextExpiry", { when: fmt.date(balance.data.value.next_expire_time, "dateTime") }) }}</span>
          </div>
          <div class="flex flex-wrap gap-2">
            <Button v-if="!locked" size="sm" data-testid="customer-grant-credits" @click="granting = true"><Coins aria-hidden="true" />{{ t("billing.credits.grant.free") }}</Button>
            <Button size="sm" variant="outline" @click="emit('credits')">{{ t("billing.customers.sheet.manageCredits") }}</Button>
          </div>
        </div>
        <GrantCreditsSheet v-model:open="granting" :tenant-id="tenantId" :customer="id" />
        <LedgerCard :tenant-id="tenantId" :customer="id" class="-mx-3 gap-2 border-0 bg-transparent p-0 shadow-none [&>h2]:pt-0" />
      </TabsContent>
      <TabsContent value="agreements" class="outline-none">
        <CustomerAgreements :tenant-id="tenantId" :external-id="id" :readonly="locked" />
      </TabsContent>
      <TabsContent value="details" class="outline-none">
        <dl class="divide-border/70 m-0 flex flex-col divide-y text-sm" data-testid="customer-details">
          <div class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.externalId") }}</dt>
            <dd class="mono m-0 break-all">{{ id }}</dd>
          </div>
          <div class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.name") }}</dt>
            <dd class="m-0 break-words" dir="auto">{{ customer.name || "—" }}</dd>
          </div>
          <div class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.email") }}</dt>
            <dd class="m-0 break-all">{{ maskEmail(customer.email) || "—" }}</dd>
          </div>
          <div v-if="customer.phone" class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.profile.phone") }}</dt>
            <dd class="m-0 break-all" dir="ltr" data-testid="customer-phone">{{ customer.phone }}</dd>
          </div>
          <div class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.country") }}</dt>
            <dd class="m-0">{{ customer.country ? `${flagOf(customer.country)} ${country}` : "—" }}</dd>
          </div>
          <div class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.currency") }}</dt>
            <dd class="m-0">{{ customer.billing_currency || t("billing.customers.tenantCurrency") }}</dd>
          </div>
          <div class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.columns.created") }}</dt>
            <dd class="m-0">{{ fmt.date(customer.create_time, "dateTime", "—") }}</dd>
          </div>
          <div class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.address.title") }}</dt>
            <dd class="m-0 flex items-start justify-between gap-3">
              <span class="break-words" dir="auto">{{ address || "—" }}</span>
              <Button v-if="!locked" size="xs" variant="ghost" :aria-label="t('billing.customers.address.edit')" @click="emit('editAddress')"><Pencil aria-hidden="true" /></Button>
            </dd>
          </div>
          <div class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("einvoice.customer.title") }}</dt>
            <dd class="m-0 flex items-start justify-between gap-3">
              <span class="flex flex-col gap-0.5 break-words">
                <span>{{ tax || "—" }}</span>
                <span v-if="customer.tax_id_warning" class="text-warning text-xs" data-testid="customer-tax-warning">{{ t("einvoice.customer.warned") }}</span>
                <span v-if="customer.e_invoice_registered" class="text-muted-foreground text-xs">{{ t("einvoice.customer.registered") }}</span>
              </span>
              <Button v-if="!locked" size="xs" variant="ghost" :aria-label="t('einvoice.customer.edit')" @click="emit('editTax')"><Pencil aria-hidden="true" /></Button>
            </dd>
          </div>
          <div v-if="customer.payment_method" class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.sheet.paymentMethod") }}</dt>
            <dd class="mono m-0 break-all">{{ customer.payment_method }}</dd>
          </div>
        </dl>
        <!-- Who blocked or lifted, when and why: kept for good, newest first. -->
        <section class="mt-6 flex flex-col gap-2" :aria-label="t('billing.customers.block.history')" data-testid="customer-block-history">
          <h3 class="m-0 text-sm font-semibold">{{ t("billing.customers.block.history") }}</h3>
          <Skeleton v-if="blockEvents.isPending.value" class="h-5 w-40" />
          <ErrorState v-else-if="blockEvents.error.value" compact class="mx-0 mb-0" :error="blockEvents.error.value" :retrying="blockEvents.isFetching.value" @retry="blockEvents.refetch()" />
          <p v-else-if="(blockEvents.data.value?.events ?? []).length === 0" class="text-muted-foreground m-0 text-sm">{{ t("billing.customers.block.historyEmpty") }}</p>
          <ul v-else class="divide-border/70 m-0 flex list-none flex-col divide-y p-0 text-sm">
            <li v-for="e in blockEvents.data.value?.events" :key="e.id" class="flex flex-col gap-0.5 py-2" data-testid="customer-block-event">
              <span class="font-medium">{{ t(`billing.customers.block.actions.${e.action}`) }}<template v-if="e.category"> · {{ categoryText(e.category) }}</template></span>
              <span v-if="e.reason" class="text-muted-foreground break-words" dir="auto">{{ e.reason }}</span>
              <span v-if="e.suspend_service" class="text-muted-foreground text-xs">{{ t("billing.customers.block.suspended") }}</span>
              <span v-if="e.blocked_until" class="text-muted-foreground text-xs">{{ t("billing.customers.block.bannerUntil", { date: fmt.date(e.blocked_until, "day") }) }}</span>
              <span class="text-muted-foreground text-xs">{{ fmt.date(e.create_time, "dateTime") }}<template v-if="e.actor"> · {{ t("billing.customers.block.by", { actor: e.actor }) }}</template></span>
            </li>
          </ul>
        </section>
      </TabsContent>
    </Tabs>
  </div>
</template>
