<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { formatMinor } from "@/shared/lib/money";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import type { AppOverview } from "./api";

/** The newest payments of the window, newest first: when, status, provider and amount. Scrolls sideways inside the card on a narrow screen. */
const props = defineProps<{ payments: AppOverview["payments"] | undefined }>();
const { t, locale } = useI18n();

const when = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium", timeStyle: "short" }).format(new Date(v)) : "");
</script>

<template>
  <section class="surface flex flex-col gap-3 p-6" :aria-label="t('product.payments.title')">
    <div class="flex items-baseline justify-between gap-3">
      <h2 class="text-lg font-semibold tracking-tight">{{ t("product.payments.title") }}</h2>
      <span v-if="props.payments?.truncated" class="text-muted-foreground text-xs">{{ t("product.payments.truncated") }}</span>
    </div>
    <p v-if="!props.payments" class="text-muted-foreground py-8 text-center text-sm">{{ t("product.unavailable") }}</p>
    <p v-else-if="props.payments.recent?.length === 0" class="text-muted-foreground py-8 text-center text-sm">{{ t("product.payments.empty") }}</p>
    <div v-else class="-mx-2 overflow-x-auto px-2">
      <table class="w-full text-sm">
        <thead>
          <tr class="text-muted-foreground text-start text-xs">
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.payments.when") }}</th>
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.payments.status") }}</th>
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.payments.provider") }}</th>
            <th scope="col" class="py-2 text-end font-medium">{{ t("product.payments.amount") }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in props.payments.recent" :key="p.id" class="border-border/70 border-t">
            <td class="py-3 pe-4 whitespace-nowrap">{{ when(p.occurred_at) }}</td>
            <td class="py-3 pe-4"><StatusPill :tone="toneOf(p.status)">{{ t(`product.status.${p.status}`) }}</StatusPill></td>
            <td class="text-muted-foreground py-3 pe-4 capitalize">{{ p.provider }}</td>
            <td class="py-3 text-end font-medium whitespace-nowrap tabular">
              {{ p.status === "refunded" ? "−" : "" }}{{ formatMinor(p.amount_minor, p.currency, locale) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
