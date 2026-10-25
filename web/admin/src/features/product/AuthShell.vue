<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { Donut } from "@/shared/ui/donut";
import { Sparkline } from "@/shared/ui/sparkline";
import { StatusPill } from "@/shared/ui/status-pill";
import { brandName } from "@/shared/lib/brand";

/** The frame of the public pages: a big rounded card, the form on one side and, from a tablet up, a pastel panel that shows what the product looks like. */
const { t } = useI18n();
const brand = brandName();
defineProps<{ title: string; intro: string }>();
</script>

<template>
  <div class="surface grid w-full max-w-[980px] overflow-hidden md:grid-cols-[1.05fr_1fr]">
    <section class="flex flex-col justify-center gap-6 p-7 sm:p-10">
      <div>
        <h1 class="text-3xl font-semibold tracking-tight">{{ title }}</h1>
        <p class="text-muted-foreground mt-2 text-sm leading-relaxed">{{ intro }}</p>
      </div>
      <slot />
      <div class="text-muted-foreground text-sm"><slot name="footer" /></div>
    </section>
    <aside class="from-brand-soft to-brand/40 relative hidden flex-col justify-between gap-8 overflow-hidden bg-gradient-to-br p-10 md:flex" aria-hidden="true">
      <div>
        <p class="text-brand-strong text-sm font-medium">{{ brand }}</p>
        <p class="text-foreground mt-2 max-w-xs text-2xl leading-snug font-semibold tracking-tight">{{ t("product.hero.title") }}</p>
      </div>
      <div class="bg-card/80 flex items-center gap-5 rounded-3xl p-5 shadow-card backdrop-blur">
        <Donut
          :size="104"
          :thickness="13"
          label=""
          :segments="[
            { key: 'a', value: 68, color: 'var(--brand)' },
            { key: 'b', value: 22, color: 'var(--chart-processing)' },
            { key: 'c', value: 10, color: 'var(--chart-failed)' },
          ]"
        >
          <span class="text-lg font-semibold tabular">68%</span>
        </Donut>
        <div class="flex flex-col gap-2">
          <StatusPill tone="success">{{ t("product.status.paid") }}</StatusPill>
          <StatusPill tone="warning">{{ t("product.status.processing") }}</StatusPill>
          <StatusPill tone="danger">{{ t("product.status.failed") }}</StatusPill>
        </div>
      </div>
      <div class="bg-card/80 flex items-end justify-between gap-4 rounded-3xl p-5 shadow-card backdrop-blur">
        <div>
          <p class="text-muted-foreground text-xs">{{ t("product.kpi.revenue") }}</p>
          <p class="text-2xl font-semibold tabular">$24,310</p>
        </div>
        <Sparkline :values="[2, 5, 3, 8, 6, 9, 7, 12, 10, 14]" :width="130" :height="44" />
      </div>
    </aside>
  </div>
</template>
