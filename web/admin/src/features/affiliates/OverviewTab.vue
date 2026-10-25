<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { PRESETS, presetRange, validRange, type Preset, type Range } from "@/features/accounting/period";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useBalances, useFunnel, useProgram } from "./api";
import { funnelCommissions, funnelSteps, programTone, sumBalances } from "./logic";

const props = defineProps<{ tenantId: string }>();
defineEmits<{ goto: [tab: string] }>();
const { t } = useI18n();
const fmt = useFormat();
const tenantId = computed(() => props.tenantId);

const program = useProgram(tenantId);
const preset = ref<Preset>("last30");
const custom = ref<Range>({ from: "", to: "" });
const range = computed(() => presetRange(preset.value, new Date(), custom.value));
const rangeOk = computed(() => validRange(range.value));
const funnel = useFunnel(
  tenantId,
  () => range.value.from,
  () => range.value.to,
  rangeOk,
);
const balances = useBalances(tenantId);

const steps = computed(() => funnelSteps(funnel.data.value));
const commissions = computed(() => funnelCommissions(funnel.data.value));
const sums = computed(() => sumBalances(balances.data.value));
</script>

<template>
  <ErrorText :error="program.error.value" />
  <div v-if="program.isSuccess.value && program.data.value === null" data-testid="no-program" role="status" class="bg-brand-soft text-brand-strong rounded-2xl px-4 py-3 text-sm">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <span>{{ t("affiliates.overview.noProgram") }}</span>
      <Button type="button" size="sm" @click="$emit('goto', 'program')">{{ t("affiliates.overview.setUp") }}</Button>
    </div>
  </div>
  <p v-else-if="program.data.value && program.data.value.status !== 'active'" class="text-muted-foreground flex flex-wrap items-center gap-2 text-sm" role="status">
    <StatusPill :tone="programTone(program.data.value.status)">{{ t(`affiliates.status.program.${program.data.value.status}`) }}</StatusPill>
    {{ t("affiliates.overview.inactive") }}
  </p>

  <Card>
    <div class="flex flex-wrap items-center gap-2" role="group" :aria-label="t('billing.accounting.period.label')">
      <Button v-for="p in PRESETS" :key="p" type="button" size="sm" :variant="preset === p ? 'default' : 'outline'" :aria-pressed="preset === p" @click="preset = p">
        {{ t(`billing.accounting.period.${p}`) }}
      </Button>
      <template v-if="preset === 'custom'">
        <Input v-model="custom.from" type="date" class="w-40" :aria-label="t('billing.accounting.period.from')" />
        <Input v-model="custom.to" type="date" class="w-40" :aria-label="t('billing.accounting.period.to')" />
      </template>
    </div>
    <p v-if="preset === 'custom' && !rangeOk" class="text-muted-foreground text-sm">{{ t("billing.accounting.period.invalid") }}</p>

    <h2 class="text-lg font-semibold">{{ t("affiliates.overview.funnel") }}</h2>
    <ErrorText :error="funnel.error.value" />
    <Skeleton v-if="funnel.isPending.value && rangeOk" class="h-28" />
    <ol v-else-if="funnel.data.value" class="m-0 grid list-none gap-4 p-0 sm:grid-cols-2 lg:grid-cols-4" data-testid="funnel">
      <li v-for="(s, i) in steps" :key="s.key" :data-step="s.key" class="bg-muted/50 flex min-w-0 flex-col gap-1 rounded-2xl p-4">
        <span class="text-muted-foreground text-sm">{{ i + 1 }}. {{ t(`affiliates.overview.steps.${s.key}`) }}</span>
        <span class="text-2xl font-semibold tabular-nums">{{ fmt.count(s.count) }}</span>
        <span v-if="s.rate !== null" class="text-muted-foreground text-xs">{{ t("affiliates.overview.rate", { pct: fmt.percent(s.rate) }) }}</span>
      </li>
      <li data-step="commission" class="bg-muted/50 flex min-w-0 flex-col gap-1 rounded-2xl p-4">
        <span class="text-muted-foreground text-sm">4. {{ t("affiliates.overview.steps.commission") }}</span>
        <p v-if="!commissions.length" class="text-muted-foreground m-0 text-sm">{{ t("affiliates.overview.noCommission") }}</p>
        <div v-for="c in commissions" :key="c.currency" :data-currency="c.currency" class="flex flex-col">
          <span class="text-2xl font-semibold tabular-nums">{{ fmt.amount(c.earned, c.currency) }}</span>
          <span v-if="c.reversed !== '0'" class="text-destructive text-xs tabular-nums">{{ t("affiliates.overview.reversed", { amount: fmt.amount(c.reversed, c.currency) }) }}</span>
        </div>
      </li>
    </ol>
    <p class="text-muted-foreground text-xs">{{ t("affiliates.overview.periodNote") }}</p>
  </Card>

  <h2 class="text-lg font-semibold">{{ t("affiliates.overview.balances") }}</h2>
  <p class="text-muted-foreground -mt-4 text-sm">{{ t("affiliates.overview.balancesHint") }}</p>
  <ErrorText :error="balances.error.value" />
  <Skeleton v-if="balances.isPending.value" class="h-28" />
  <p v-else-if="balances.isSuccess.value && !sums.length" class="text-muted-foreground text-sm">{{ t("affiliates.overview.noBalances") }}</p>
  <section v-for="c in sums" :key="c.currency" :aria-label="c.currency">
    <Card>
      <h3 class="text-lg font-semibold">{{ c.currency }}</h3>
      <dl class="m-0 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div data-stat="pending">
          <dt class="text-muted-foreground text-sm" :title="t('affiliates.overview.pendingHint')">{{ t("affiliates.overview.pending") }}</dt>
          <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.pending, c.currency) }}</dd>
        </div>
        <div data-stat="available">
          <dt class="text-muted-foreground text-sm">{{ t("affiliates.overview.available") }}</dt>
          <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.approved, c.currency) }}</dd>
        </div>
        <div data-stat="paid">
          <dt class="text-muted-foreground text-sm">{{ t("affiliates.overview.paid") }}</dt>
          <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.paid, c.currency) }}</dd>
        </div>
        <div data-stat="reversed">
          <dt class="text-muted-foreground text-sm">{{ t("affiliates.overview.takenBack") }}</dt>
          <dd class="m-0 text-xl font-semibold tabular-nums">{{ fmt.amount(c.reversed, c.currency) }}</dd>
        </div>
      </dl>
    </Card>
  </section>
</template>
