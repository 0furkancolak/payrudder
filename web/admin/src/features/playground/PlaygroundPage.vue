<script setup lang="ts">
import PeekInvoice from "@/features/related/PeekInvoice.vue";
import { Link } from "@tanstack/vue-router";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { sagaVariant, statusVariant } from "@/shared/lib/badges";
import { formatMinor } from "@/shared/lib/money";
import { playgroundDefaults, prefillPlayground } from "@/shared/lib/prefill";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { NativeSelect } from "@/shared/ui/native-select";
import { HeaderAction, PageHeader } from "@/shared/ui/page-header";
import { Table, TableBody, TableCell, TableRow } from "@/shared/ui/table";
import type { Currency } from "@/shared/lib/currencies";
import { SCENARIOS, usePlayground, type Scenario } from "./usePlayground";
import { CurrencySelect, TimezoneSelect } from "@/shared/ui/search-select";

const { t, locale } = useI18n();
const play = usePlayground();

// In a development stack the account form comes pre-filled (see shared/lib/prefill.ts).
const prefill = prefillPlayground();
const name = ref(prefill ? playgroundDefaults.name : "");
const currency = ref<Currency | "">(prefill ? playgroundDefaults.currency : "");
const timezone = ref(prefill ? playgroundDefaults.timezone : "");
const graceHours = ref(prefill ? playgroundDefaults.graceHours : "");
const quantity = ref("42");
const creating = ref(false);

function create() {
  if (currency.value === "") return;
  play.createAccount.mutate(
    { name: name.value, currency: currency.value, timezone: timezone.value, graceHours: Number(graceHours.value) },
    { onSuccess: () => (creating.value = false) },
  );
}
</script>

<template>
  <PageHeader :title="t('playground.title')" :description="t('playground.intro')" />
  <HeaderAction v-if="play.tenantId.value === null" test-id="playground-account-open" :label="t('playground.account.create')" @click="creating = true" />
  <ErrorText :error="play.error.value" class="bg-destructive/10 mb-4 rounded-2xl px-4 py-3" />

  <div class="grid gap-4 lg:grid-cols-2">
    <Card>
      <h2 class="text-base font-semibold">{{ t("playground.account.title") }}</h2>
      <i18n-t v-if="play.tenantId.value !== null" keypath="playground.account.ready" tag="p" class="text-success bg-success/10 rounded-2xl px-4 py-3 text-sm" scope="global">
        <template #tenant>
          <Link to="/tenants/$id" :params="{ id: play.tenantId.value }" class="mono underline">{{ play.tenantId.value }}</Link>
        </template>
        <template #key>
          <code>{{ play.key.value?.slice(0, 16) }}…</code>
        </template>
      </i18n-t>
    </Card>

    <Card>
      <h2 class="text-base font-semibold">{{ t("playground.setup.title") }}</h2>
      <div class="flex flex-wrap items-center gap-3">
        <Button type="button" :disabled="play.busy.value || play.key.value === null || play.ready.value" @click="play.setUp.mutate()">
          {{ t("playground.setup.button") }}
        </Button>
        <label class="text-muted-foreground flex items-center gap-2 text-sm">
          {{ t("playground.setup.outcome") }}
          <NativeSelect
            :aria-label="t('playground.setup.outcome')"
            :disabled="!play.ready.value || play.busy.value"
            :model-value="play.scenario.value"
            @change="play.chooseScenario.mutate(($event.target as HTMLSelectElement).value as Scenario)"
          >
            <option v-for="s in SCENARIOS" :key="s" :value="s">{{ t(`playground.scenarios.${s}`) }}</option>
          </NativeSelect>
        </label>
      </div>
    </Card>

    <Card>
      <h2 class="text-base font-semibold">{{ t("playground.usage.title") }}</h2>
      <div class="flex flex-wrap items-center gap-3">
        <Input v-model="quantity" type="number" min="1" class="w-32" :aria-label="t('playground.usage.calls')" />
        <Button type="button" :disabled="play.busy.value || !play.ready.value || play.closed.value" @click="play.sendUsage.mutate(Number(quantity))">
          {{ t("playground.usage.send") }}
        </Button>
        <span class="text-muted-foreground text-sm" data-testid="sent">{{ t(play.sent.value === 1 ? "playground.usage.sentOne" : "playground.usage.sent", { n: new Intl.NumberFormat(locale).format(play.sent.value) }) }}</span>
      </div>
    </Card>

    <Card>
      <h2 class="text-base font-semibold">{{ t("playground.close.title") }}</h2>
      <div class="flex flex-wrap items-center gap-3">
        <Button type="button" :disabled="play.busy.value || !play.ready.value || play.sent.value === 0 || play.closed.value" @click="play.closePeriod.mutate()">
          {{ t("playground.close.button") }}
        </Button>
        <span class="text-muted-foreground text-sm">{{ t("playground.close.hint") }}</span>
      </div>
    </Card>
  </div>

  <Sheet v-model:open="creating" name="new-playground">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("playground.account.title") }}</SheetTitle>
        <SheetDescription>{{ t("playground.intro") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="playground-account" class="grid gap-3" role="form" :aria-label="t('playground.account.formLabel')" @submit.prevent="create">
          <Input v-model="name" :aria-label="t('playground.account.name')" :placeholder="t('playground.account.name')" required />
          <CurrencySelect v-model="currency" :aria-label="t('playground.account.currency')" />
          <TimezoneSelect v-model="timezone" :aria-label="t('playground.account.timezone')" />
          <Input v-model="graceHours" type="number" min="0" max="168" :aria-label="t('playground.account.grace')" :placeholder="t('playground.account.gracePlaceholder')" required />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="creating = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="playground-account" :disabled="play.busy.value">{{ t("playground.account.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <Card v-if="play.closed.value" class="mt-4 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("playground.invoice.title") }}</h2>
    <p v-if="(play.invoices.data.value ?? []).length === 0" class="text-muted-foreground px-3 pb-3 text-sm">{{ t("playground.invoice.waiting") }}</p>
    <Table>
      <TableBody>
        <TableRow v-for="inv in play.invoices.data.value ?? []" :key="inv.id" data-testid="playground-invoice">
          <TableCell class="font-medium">{{ inv.number }}</TableCell>
          <TableCell><Badge :variant="sagaVariant(inv.saga_state)" class="mono">{{ inv.saga_state }}</Badge></TableCell>
          <TableCell><Badge :variant="statusVariant(inv.status)">{{ inv.status }}</Badge></TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</TableCell>
          <TableCell class="text-end">
            <PeekInvoice v-if="play.tenantId.value && inv.id" :id="inv.id" :tenant-id="play.tenantId.value" class="font-medium underline">
              {{ t("playground.invoice.open") }}
            </PeekInvoice>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>

  <Card v-if="play.log.value.length > 0" class="mt-4">
    <h2 class="text-lg font-semibold">{{ t("playground.log.title") }}</h2>
    <ul class="text-muted-foreground space-y-1 text-sm">
      <li v-for="(line, i) in play.log.value" :key="i" class="flex gap-2"><span class="bg-muted-foreground/50 mt-2 size-1.5 shrink-0 rounded-full" />{{ t(line.key === "playground.log.usage" && line.params?.n === 1 ? "playground.log.usageOne" : line.key, line.params ?? {}) }}</li>
    </ul>
  </Card>
</template>
