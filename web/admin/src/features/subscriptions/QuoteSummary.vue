<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { changeOutcome } from "./lib";

/** The price of a change of plan, before it is made: the credit, the charge, what that comes to now, and the next renewal (and, for Quotify's own, when it takes effect). */
const props = defineProps<{
  quote: { credit_minor?: string; charge_minor?: string; net_minor?: string; next_renewal_minor?: string; next_renewal_time?: string; effective_time?: string; timing?: string };
  currency: string | undefined;
  /** Quotify's own subscriptions say when the change takes effect and the date of the next renewal; a provider's quote has neither. */
  detailed?: boolean;
}>();
const { t } = useI18n();
const fmt = useFormat();

const money = (v?: string) => fmt.money(v, props.currency);
const date = (v?: string | null) => fmt.date(v, "day", "—");
const outcome = computed(() => {
  const o = changeOutcome(props.quote.net_minor);
  if (o.kind === "nothing") return t("billing.subscriptions.change.nothing");
  return t(o.kind === "credit" ? "billing.subscriptions.change.creditAdded" : "billing.subscriptions.change.due", { amount: money(o.minor) });
});
</script>

<template>
  <dl class="bg-secondary/50 grid gap-1 rounded-2xl px-4 py-3 text-sm sm:grid-cols-2" :aria-label="t('billing.subscriptions.change.quote')">
    <template v-if="detailed">
      <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.effective") }}</dt>
      <dd>{{ quote.timing === "CHANGE_TIMING_PERIOD_END" ? t("billing.subscriptions.change.atEnd") : t("billing.subscriptions.change.now") }} · {{ date(quote.effective_time) }}</dd>
    </template>
    <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.credit") }}</dt>
    <dd class="tabular-nums">{{ money(quote.credit_minor) }}</dd>
    <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.charge") }}</dt>
    <dd class="tabular-nums">{{ money(quote.charge_minor) }}</dd>
    <dt class="font-medium">{{ t("billing.subscriptions.change.result") }}</dt>
    <dd class="font-medium tabular-nums">{{ outcome }}</dd>
    <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.next") }}</dt>
    <dd class="tabular-nums">{{ money(quote.next_renewal_minor) }}<template v-if="detailed"> · {{ date(quote.next_renewal_time) }}</template></dd>
  </dl>
</template>
