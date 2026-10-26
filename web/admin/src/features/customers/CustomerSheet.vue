<script setup lang="ts">
import { Check, Copy, CreditCard, Link2, MapPin, Pencil, Plus, Receipt } from "@lucide/vue";
import { TabsContent } from "reka-ui";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useCreditBalance } from "@/features/credits/api";
import { formatCredits } from "@/features/credits/lib";
import LedgerCard from "@/features/credits/LedgerCard.vue";
import CustomerLinksCard from "@/features/paymentlinks/CustomerLinksCard.vue";
import { CustomerRelated, InvoiceRows, PaymentRows, SubscriptionRows, useCustomerInvoices } from "@/features/related";
import { copyText } from "@/shared/lib/clipboard";
import { useFormat } from "@/shared/lib/format";
import { formatMinor, maskEmail } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/shared/ui/dropdown-menu";
import { Skeleton } from "@/shared/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import { addressLine } from "./api";
import CustomerAvatar from "./CustomerAvatar.vue";
import { countryName, flagOf, headline, type Customer, type CustomerSummary, type CustomerTab } from "./lib";

/**
 * The body of a customer's sheet, after Stripe's customer page: who they are (avatar, e-mail, id to copy, country, since when), what can be done for
 * them, four figures (spend, what is open, credits, subscriptions that bill now) and a tab per kind of record. The tab and the payment-page card are
 * models, so a row action of the list can open the sheet on them. The figures come from the list page (`summary`), so list and sheet agree.
 */

const props = defineProps<{ tenantId: string; customer: Customer; summary?: CustomerSummary; summaryLoading?: boolean }>();
const emit = defineEmits<{ editAddress: []; editTax: []; newSubscription: []; paymentLink: []; credits: [] }>();
const tab = defineModel<CustomerTab>("tab", { default: "overview" });
const portal = defineModel<boolean>("portal", { default: false });
const { t, locale } = useI18n();
const fmt = useFormat();

const id = computed(() => props.customer.external_id ?? "");
const balance = useCreditBalance(() => props.tenantId, id);
const invoices = useCustomerInvoices(() => props.tenantId, id);
const invoiceIds = computed(() => (invoices.data.value?.invoices ?? []).map((i) => i.id ?? ""));

const TABS: CustomerTab[] = ["overview", "payments", "invoices", "subscriptions", "credits", "details"];
const spend = computed(() => (props.summary ? headline(props.summary.spend, props.customer.billing_currency) : undefined));
const open = computed(() => (props.summary ? headline(props.summary.open, props.customer.billing_currency) : undefined));
const zero = computed(() => formatMinor(0, props.customer.billing_currency || spend.value?.currency || open.value?.currency, locale.value) || "0");
const money = (h?: { minor: string; currency: string }) => (h ? formatMinor(h.minor, h.currency, locale.value) : zero.value);

const copied = ref(false);
async function copyId() {
  copied.value = await copyText(id.value);
  if (copied.value) setTimeout(() => (copied.value = false), 1500);
}
const country = computed(() => countryName(props.customer.country, locale.value));
const address = computed(() => addressLine(props.customer.address));
const tax = computed(() => [props.customer.tax_id, props.customer.tax_office].filter(Boolean).join(" · "));
function showPortal() {
  tab.value = "payments";
  portal.value = !portal.value;
}
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

    <!-- What can be done -->
    <div class="flex flex-wrap gap-2" role="group" :aria-label="t('billing.customers.sheet.actions')">
      <Button size="sm" data-testid="customer-new-subscription" @click="emit('newSubscription')"><Plus aria-hidden="true" />{{ t("billing.customers.sheet.newSubscription") }}</Button>
      <Button size="sm" variant="outline" @click="emit('paymentLink')"><Link2 aria-hidden="true" />{{ t("billing.paymentLinks.paymentLink") }}</Button>
      <Button size="sm" variant="outline" :aria-pressed="portal" @click="showPortal"><Receipt aria-hidden="true" />{{ t("billing.paymentLinks.portal.open") }}</Button>
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button size="sm" variant="outline" data-testid="customer-edit"><Pencil aria-hidden="true" />{{ t("billing.customers.sheet.edit") }}</Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" class="min-w-44">
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

    <CustomerLinksCard v-if="portal" :key="id" :tenant-id="tenantId" :customer="id" class="mb-0" @close="portal = false" />

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

      <TabsContent value="overview" class="outline-none">
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
        <Button size="sm" variant="outline" class="self-start" @click="emit('newSubscription')"><Plus aria-hidden="true" />{{ t("billing.customers.sheet.newSubscription") }}</Button>
      </TabsContent>
      <TabsContent value="credits" class="flex flex-col gap-4 outline-none">
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div class="flex flex-col">
            <span class="text-muted-foreground text-xs">{{ t("billing.customers.stats.credits") }}</span>
            <span class="text-2xl font-semibold tabular-nums">{{ balance.data.value ? formatCredits(balance.data.value.balance, locale) : "—" }}</span>
            <span v-if="balance.data.value?.next_expire_time" class="text-muted-foreground text-xs">{{ t("billing.credits.customer.nextExpiry", { when: fmt.date(balance.data.value.next_expire_time, "dateTime") }) }}</span>
          </div>
          <Button size="sm" variant="outline" @click="emit('credits')">{{ t("billing.customers.sheet.manageCredits") }}</Button>
        </div>
        <LedgerCard :tenant-id="tenantId" :customer="id" class="-mx-3 gap-2 border-0 bg-transparent p-0 shadow-none [&>h2]:pt-0" />
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
              <Button size="xs" variant="ghost" :aria-label="t('billing.customers.address.edit')" @click="emit('editAddress')"><Pencil aria-hidden="true" /></Button>
            </dd>
          </div>
          <div class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("einvoice.customer.title") }}</dt>
            <dd class="m-0 flex items-start justify-between gap-3">
              <span class="flex flex-col gap-0.5 break-words">
                <span>{{ tax || "—" }}</span>
                <span v-if="customer.e_invoice_registered" class="text-muted-foreground text-xs">{{ t("einvoice.customer.registered") }}</span>
              </span>
              <Button size="xs" variant="ghost" :aria-label="t('einvoice.customer.edit')" @click="emit('editTax')"><Pencil aria-hidden="true" /></Button>
            </dd>
          </div>
          <div v-if="customer.payment_method" class="grid gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt class="text-muted-foreground">{{ t("billing.customers.sheet.paymentMethod") }}</dt>
            <dd class="mono m-0 break-all">{{ customer.payment_method }}</dd>
          </div>
        </dl>
      </TabsContent>
    </Tabs>
  </div>
</template>
