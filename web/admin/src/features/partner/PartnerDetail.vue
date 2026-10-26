<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { validRange } from "@/features/accounting/period";
import { rangeOf } from "@/shared/lib/dateRange";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { useFormat } from "@/shared/lib/format";
import { Card } from "@/shared/ui/card";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import { useAffiliation } from "./api";
import CommissionsTab from "./CommissionsTab.vue";
import { affiliateTone, earnedByCurrency, environmentTone, sumBalances } from "./logic";
import PayoutsTab from "./PayoutsTab.vue";
import ReferralsTab from "./ReferralsTab.vue";
import SettingsTab from "./SettingsTab.vue";
import StatusBanner from "./StatusBanner.vue";
import TermsList from "./TermsList.vue";

/** One affiliation, as the body of the sheet the portal's home opens: the link, the figures of a period, the balances per currency, the seller's terms, and the partner's own lists. */
const TABS = ["referrals", "commissions", "payouts", "settings"] as const;
type Tab = (typeof TABS)[number];

const { t } = useI18n();
const fmt = useFormat();
const props = defineProps<{ id: string }>();
const id = computed(() => props.id);
const tab = ref<Tab>("referrals");

const range = ref<DateRange>(rangeOf("30d", new Date()));
const rangeOk = computed(() => validRange(range.value));
const detail = useAffiliation(
  id,
  () => range.value.from,
  () => range.value.to,
  rangeOk,
);
const a = computed(() => detail.data.value?.affiliation);
const earned = computed(() => earnedByCurrency(detail.data.value?.earned));
const balances = computed(() => sumBalances(detail.data.value?.balances));
</script>

<template>
  <div class="flex flex-col gap-6">
    <ErrorText :error="detail.error.value" />
    <Skeleton v-if="detail.isPending.value" class="h-40" />

    <template v-if="a">
      <div class="flex flex-wrap gap-1.5">
        <StatusPill :tone="environmentTone(a.environment)" data-testid="env-badge">{{ t(`partner.environment.${a.environment}`) }}</StatusPill>
        <StatusPill :tone="affiliateTone(a.status)" data-testid="status-badge">{{ t(`partner.status.${a.status}`) }}</StatusPill>
      </div>
      <StatusBanner :affiliation="a" />
      <CopyField v-if="a.link" :value="a.link" :label="t('partner.link')" :copy-label="t('partner.copyLink')" testid="affiliation-link" />

      <Card>
        <div class="flex flex-wrap items-center gap-2" role="group" :aria-label="t('billing.accounting.period.label')">
          <DateRangePicker v-model="range" />
        </div>
        <p v-if="!rangeOk" class="text-muted-foreground m-0 text-sm">{{ t("billing.accounting.period.invalid") }}</p>
        <dl class="m-0 grid gap-4 sm:grid-cols-3" data-testid="figures">
          <div data-stat="clicks">
            <dt class="text-muted-foreground text-sm">{{ t("partner.stats.clicks") }}</dt>
            <dd class="m-0 text-2xl font-semibold tabular-nums">{{ fmt.count(Number(detail.data.value?.clicks ?? 0)) }}</dd>
          </div>
          <div data-stat="referrals">
            <dt class="text-muted-foreground text-sm">{{ t("partner.stats.referrals") }}</dt>
            <dd class="m-0 text-2xl font-semibold tabular-nums">{{ fmt.count(Number(detail.data.value?.referrals ?? 0)) }}</dd>
          </div>
          <div data-stat="paying">
            <dt class="text-muted-foreground text-sm">{{ t("partner.stats.paying") }}</dt>
            <dd class="m-0 text-2xl font-semibold tabular-nums">{{ fmt.count(Number(detail.data.value?.paying_customers ?? 0)) }}</dd>
          </div>
        </dl>
        <h2 class="m-0 text-base font-semibold">{{ t("partner.earned.title") }}</h2>
        <p v-if="!earned.length" class="text-muted-foreground m-0 text-sm">{{ t("partner.earned.none") }}</p>
        <ul v-else class="m-0 grid list-none gap-3 p-0 sm:grid-cols-2" data-testid="earned">
          <li v-for="e in earned" :key="e.currency" :data-currency="e.currency" class="bg-muted/50 flex flex-col rounded-2xl p-4">
            <span class="text-2xl font-semibold tabular-nums">{{ fmt.amount(e.earned, e.currency) }}</span>
            <span v-if="e.reversed !== '0'" class="text-destructive text-xs tabular-nums">{{ t("partner.earned.takenBack", { amount: fmt.amount(e.reversed, e.currency) }) }}</span>
          </li>
        </ul>
        <p class="text-muted-foreground m-0 text-xs">{{ t("partner.earned.note") }}</p>
      </Card>

      <h2 class="m-0 text-lg font-semibold">{{ t("partner.balances.title") }}</h2>
      <p v-if="!balances.length" class="text-muted-foreground m-0 text-sm">{{ t("partner.balances.none") }}</p>
      <section v-for="c in balances" :key="c.currency" :aria-label="c.currency">
        <Card>
          <h3 class="m-0 text-lg font-semibold">{{ c.currency }}</h3>
          <dl class="m-0 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div data-stat="pending">
              <dt class="text-muted-foreground text-sm" :title="t('partner.balances.pendingHint')">{{ t("partner.balances.pending") }}</dt>
              <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.pending, c.currency) }}</dd>
            </div>
            <div data-stat="available">
              <dt class="text-muted-foreground text-sm">{{ t("partner.balances.available") }}</dt>
              <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.approved, c.currency) }}</dd>
            </div>
            <div data-stat="paid">
              <dt class="text-muted-foreground text-sm">{{ t("partner.balances.paid") }}</dt>
              <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.paid, c.currency) }}</dd>
            </div>
            <div data-stat="reversed">
              <dt class="text-muted-foreground text-sm">{{ t("partner.balances.takenBack") }}</dt>
              <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.reversed, c.currency) }}</dd>
            </div>
          </dl>
        </Card>
      </section>

      <Card>
        <h2 class="m-0 text-lg font-semibold">{{ t("partner.terms.title") }}</h2>
        <TermsList :program="a.program" />
      </Card>

      <Tabs :model-value="tab" @update:model-value="tab = String($event) as Tab">
        <TabsList class="h-auto w-full flex-wrap justify-start" :aria-label="t('partner.tabsLabel')">
          <TabsTrigger v-for="x in TABS" :key="x" :value="x" class="flex-none">{{ t(`partner.tabs.${x}`) }}</TabsTrigger>
        </TabsList>
      </Tabs>
      <ReferralsTab v-if="tab === 'referrals'" :id="id" />
      <CommissionsTab v-else-if="tab === 'commissions'" :id="id" />
      <PayoutsTab v-else-if="tab === 'payouts'" :id="id" />
      <SettingsTab v-else :affiliation="a" />
    </template>
  </div>
</template>
