<script setup lang="ts">
import { ExternalLink } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { firstError } from "@/shared/lib/errors";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useChangePlan, usePayLink, usePlan, usePlanInvoices, usePortalLink } from "./api";
import { isPayable, isUnlimited, limitOf, offerMove, quotaPercent, quotaTone, RESOURCES, stateTone, type PlanOffer, type PlanQuota } from "./lib";

/** Organisation settings, Plan and billing: the plan, what the organisation uses of it, the plans on offer, and the invoices Payrudder issued for it. */
const { t } = useI18n();
const fmt = useFormat();
const plan = usePlan();
const data = computed(() => plan.data.value);
const enabled = computed(() => data.value?.enabled === true);
const invoices = usePlanInvoices(enabled);
const change = useChangePlan();
const payLink = usePayLink();
const portal = usePortalLink();

const quotas = computed(() => RESOURCES.map((r) => data.value?.quotas?.find((q) => q.resource === r)).filter((q): q is PlanQuota => !!q));
const current = computed(() => data.value?.offers?.find((o) => o.current) ?? data.value?.plan);
const price = (o: Pick<PlanOffer, "base_fee_minor" | "currency">) => ((o.base_fee_minor ?? 0) === 0 ? t("saas.free") : t("saas.perMonth", { price: fmt.money(o.base_fee_minor, o.currency) }));
const limitText = (limit: number | undefined) => (isUnlimited(limit) ? t("saas.unlimited") : fmt.count(limit));
const barClass = { ok: "bg-brand", warning: "bg-warning", danger: "bg-destructive" } as const;

const target = ref<PlanOffer | null>(null);
const confirming = computed({ get: () => target.value !== null, set: (v) => { if (!v) target.value = null; } });
function ask(o: PlanOffer) {
  change.reset();
  target.value = o;
}
function confirm() {
  const o = target.value;
  if (!o?.code) return;
  change.mutate(o.code, {
    onSuccess: () => {
      toast.success(t("saas.changed", { plan: o.name }));
      target.value = null;
    },
  });
}

function openUrl(url: string | undefined) {
  if (url) window.open(url, "_blank", "noopener");
}
function pay(id: string) {
  payLink.mutate(id, { onSuccess: (r) => openUrl(r.url) });
}
function openPortal() {
  portal.mutate(undefined, { onSuccess: (r) => openUrl(r.url) });
}
const trialNote = (o: PlanOffer) => ((o.trial_days ?? 0) > 0 ? t("saas.trialDays", { n: o.trial_days ?? 0 }) : "");
</script>

<template>
  <section class="flex flex-col gap-6" data-testid="plan-page">
    <header class="px-1">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("saas.title") }}</h1>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ t("saas.intro") }}</p>
    </header>
    <ErrorText :error="firstError(plan)" class="bg-destructive-soft rounded-2xl px-4 py-3" />
    <Skeleton v-if="plan.isPending.value" class="h-40 rounded-2xl" />
    <p v-else-if="!enabled" class="surface text-muted-foreground p-6 text-sm" data-testid="plan-disabled">{{ t("saas.disabled") }}</p>

    <template v-else-if="data">
      <section class="surface flex flex-wrap items-center justify-between gap-4 p-6" :aria-label="t('saas.current')" data-testid="plan-current">
        <div class="flex flex-col gap-1">
          <span class="text-muted-foreground text-xs font-medium tracking-wide uppercase">{{ t("saas.current") }}</span>
          <div class="flex flex-wrap items-center gap-3">
            <h2 class="text-2xl font-semibold tracking-tight" data-testid="plan-name">{{ data.plan?.name }}</h2>
            <StatusPill :tone="stateTone(data.state)" data-testid="plan-state">{{ t(`saas.state.${data.state ?? "none"}`) }}</StatusPill>
            <StatusPill v-if="data.overridden" tone="warning">{{ t("saas.overridden") }}</StatusPill>
          </div>
          <p class="text-muted-foreground text-sm">
            {{ data.plan ? price(data.plan) : "" }}
            <template v-if="data.state === 'trialing' && data.trial_end_time"> · {{ t("saas.trialEnds", { date: fmt.date(data.trial_end_time) }) }}</template>
          </p>
        </div>
        <Button v-if="data.can_manage" variant="outline" :disabled="portal.isPending.value" data-testid="plan-portal" @click="openPortal">
          <ExternalLink />{{ t("saas.portal") }}
        </Button>
      </section>
      <ErrorText :error="firstError(portal, payLink)" class="bg-destructive-soft rounded-2xl px-4 py-3" />

      <section class="surface flex flex-col gap-4 p-6" :aria-label="t('saas.usage')" data-testid="plan-usage">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("saas.usage") }}</h2>
        <ul class="grid gap-5 md:grid-cols-2">
          <li v-for="q in quotas" :key="q.resource" class="flex flex-col gap-2" :data-testid="`quota-${q.resource}`">
            <div class="flex flex-wrap items-baseline justify-between gap-2 text-sm">
              <span class="font-medium">{{ t(`saas.resource.${q.resource}`) }}<span v-if="q.resource === 'events'" class="text-muted-foreground font-normal"> · {{ t("saas.perMonthShort") }}</span></span>
              <span :class="quotaTone(q) === 'danger' ? 'text-destructive font-medium' : 'text-muted-foreground'">
                {{ fmt.count(q.used) }} / {{ q.unlimited ? t("saas.unlimited") : limitText(q.limit) }}
              </span>
            </div>
            <div class="bg-muted h-2 overflow-hidden rounded-full" role="progressbar" :aria-valuenow="quotaPercent(q)" aria-valuemin="0" aria-valuemax="100" :aria-label="t(`saas.resource.${q.resource}`)">
              <div :class="['h-full rounded-full', barClass[quotaTone(q)]]" :style="{ width: `${quotaPercent(q)}%` }" />
            </div>
            <div class="text-muted-foreground flex flex-wrap gap-x-3 text-xs">
              <span>{{ t(`saas.modeHint.${q.mode ?? 'hard'}`) }}</span>
              <span v-if="q.overridden">{{ t("saas.ownLimit") }}</span>
              <span v-if="q.over_limit" class="text-destructive">{{ t("saas.overLimit") }}</span>
              <span v-if="q.resets_time">{{ t("saas.resets", { date: fmt.date(q.resets_time) }) }}</span>
            </div>
          </li>
        </ul>
      </section>

      <section class="flex flex-col gap-3" :aria-label="t('saas.plans')">
        <h2 class="px-1 text-lg font-semibold tracking-tight">{{ t("saas.plans") }}</h2>
        <div class="grid gap-4 md:grid-cols-3">
          <article v-for="o in data.offers" :key="o.code" :class="['surface flex flex-col gap-4 p-6', o.current && 'ring-brand ring-2']" :data-testid="`offer-${o.code}`">
            <div>
              <div class="flex items-center justify-between gap-2">
                <h3 class="text-lg font-semibold">{{ o.name }}</h3>
                <StatusPill v-if="o.current" tone="info">{{ t("saas.currentBadge") }}</StatusPill>
              </div>
              <p class="mt-1 text-2xl font-semibold tracking-tight">{{ price(o) }}</p>
              <p v-if="trialNote(o)" class="text-muted-foreground text-xs">{{ trialNote(o) }}</p>
            </div>
            <ul class="text-muted-foreground flex flex-1 flex-col gap-1.5 text-sm">
              <li v-for="r in RESOURCES" :key="r">
                <span class="text-foreground font-medium">{{ limitText(limitOf(o, r)?.limit) }}</span> {{ t(`saas.resource.${r}`).toLowerCase() }}<template v-if="r === 'events'"> {{ t("saas.perMonthShort") }}</template>
              </li>
            </ul>
            <Button v-if="data.can_manage && !o.current" :variant="offerMove(o, current) === 'upgrade' ? 'default' : 'outline'" :data-testid="`choose-${o.code}`" @click="ask(o)">
              {{ offerMove(o, current) === "upgrade" ? t("saas.upgradeTo", { plan: o.name }) : t("saas.downgradeTo", { plan: o.name }) }}
            </Button>
          </article>
        </div>
        <p v-if="!data.can_manage" class="text-muted-foreground px-1 text-sm">{{ t("saas.readOnly") }}</p>
      </section>

      <section class="surface flex flex-col gap-4 p-6" :aria-label="t('saas.invoices')" data-testid="plan-invoices">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("saas.invoices") }}</h2>
        <Skeleton v-if="invoices.isPending.value" class="h-16 rounded-2xl" />
        <p v-else-if="!invoices.data.value?.invoices?.length" class="text-muted-foreground text-sm">{{ t("saas.noInvoices") }}</p>
        <Table v-else>
          <TableHeader>
            <TableRow>
              <TableHead>{{ t("saas.invoice.number") }}</TableHead>
              <TableHead>{{ t("saas.invoice.date") }}</TableHead>
              <TableHead class="text-end">{{ t("saas.invoice.total") }}</TableHead>
              <TableHead>{{ t("saas.invoice.status") }}</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="i in invoices.data.value?.invoices" :key="i.id">
              <TableCell class="font-medium">{{ i.number || "—" }}</TableCell>
              <TableCell>{{ fmt.date(i.finalize_time || i.create_time) }}</TableCell>
              <TableCell class="text-end">{{ fmt.money(i.total_minor, i.currency) }}</TableCell>
              <TableCell><StatusPill :tone="i.status === 'paid' ? 'success' : isPayable(i.status) ? 'warning' : 'neutral'">{{ i.status }}</StatusPill></TableCell>
              <TableCell class="text-end">
                <Button v-if="data.can_manage && isPayable(i.status)" size="sm" :disabled="payLink.isPending.value" :data-testid="`pay-${i.id}`" @click="pay(i.id ?? '')">{{ t("saas.invoice.pay") }}</Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </section>
    </template>

    <Sheet v-model:open="confirming" name="plan-change">
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{{ t("saas.confirm.title", { plan: target?.name ?? "" }) }}</SheetTitle>
          <SheetDescription>{{ target ? (offerMove(target, current) === "upgrade" ? t("saas.confirm.upgrade", { price: price(target) }) : t("saas.confirm.downgrade")) : "" }}</SheetDescription>
        </SheetHeader>
        <SheetBody>
          <p v-if="target && trialNote(target) && offerMove(target, current) === 'upgrade'" class="text-sm">{{ trialNote(target) }}</p>
          <ErrorText :error="change.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" type="button" @click="confirming = false">{{ t("common.cancel") }}</Button>
          <Button type="button" :disabled="change.isPending.value" data-testid="plan-confirm" @click="confirm">{{ t("saas.confirm.submit") }}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  </section>
</template>
