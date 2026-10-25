<script setup lang="ts">
import { CreditCard, FileText, Repeat } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { StatusPill, toneOf, type Tone } from "@/shared/ui/status-pill";
import DashSection from "./DashSection.vue";
import type { Dashboard } from "./lib";

/** The latest invoices, payments and subscription changes, newest first. */
defineProps<{ data: Dashboard }>();
const { t } = useI18n();
const fmt = useFormat();

type Item = NonNullable<Dashboard["activity"]>[number];

const ICONS = { invoice: FileText, payment: CreditCard } as const;
const iconOf = (a: Item) => ICONS[a.kind as keyof typeof ICONS] ?? Repeat;

/** The status as words: invoices, payments and subscriptions each have their own list of them. */
const STATUS_KEYS = { invoice: "product.invoiceStatus", payment: "product.status" } as const;
const statusText = (a: Item) => t(`${STATUS_KEYS[a.kind as keyof typeof STATUS_KEYS] ?? "dash.activity.state"}.${a.status}`);

const STATE_TONES: Record<string, Tone> = { past_due: "warning", cancelled: "neutral" };
const toneFor = (a: Item): Tone => STATE_TONES[a.status ?? ""] ?? toneOf(a.status);

function describe(a: Item): string {
  if (a.kind === "invoice") return t("dash.activity.invoice", { label: a.label ?? "" });
  if (a.kind === "payment") return a.label === "refund" ? t("dash.activity.refund") : t("dash.activity.payment");
  return t("dash.activity.subscription", { plan: a.label ?? "" });
}

/** A refunded payment is shown as a negative amount. */
const amountOf = (a: Item) => (a.kind === "payment" && a.status === "refunded" ? `-${a.amount_minor}` : a.amount_minor);
</script>

<template>
  <DashSection :title="t('dash.activity.title')" testid="activity" class="lg:col-span-2 xl:col-span-1" :empty="(data.activity ?? []).length === 0" :empty-text="t('dash.activity.empty')">
    <ul class="divide-border/70 divide-y">
      <li v-for="a in data.activity" :key="`${a.kind}-${a.ref}`" class="flex items-center gap-3 py-2.5">
        <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><component :is="iconOf(a)" class="size-4" /></span>
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm font-medium">{{ describe(a) }}</p>
          <p class="text-muted-foreground truncate text-xs">{{ a.customer ? `${a.customer} · ` : "" }}{{ fmt.date(a.at, "dateTime", "—") }}</p>
        </div>
        <div class="flex flex-col items-end gap-1">
          <span v-if="a.amount_minor != null" class="text-sm font-medium tabular-nums">{{ fmt.money(amountOf(a), a.currency) }}</span>
          <StatusPill :tone="toneFor(a)">{{ statusText(a) }}</StatusPill>
        </div>
      </li>
    </ul>
  </DashSection>
</template>
