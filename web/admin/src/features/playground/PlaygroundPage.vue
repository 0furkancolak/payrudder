<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { CURRENCIES, SCENARIOS, usePlayground, type Currency, type Scenario } from "./usePlayground";

const { t, locale } = useI18n();
const play = usePlayground();

const name = ref("");
const currency = ref<Currency | "">("");
const timezone = ref("");
const graceHours = ref("");
const quantity = ref("42");

function create() {
  if (currency.value === "") return;
  play.createAccount.mutate({ name: name.value, currency: currency.value, timezone: timezone.value, graceHours: Number(graceHours.value) });
}
</script>

<template>
  <h1>{{ t("playground.title") }}</h1>
  <p class="muted">{{ t("playground.intro") }}</p>
  <p v-if="play.error.value" class="error" role="alert">{{ toApiError(play.error.value).message }}</p>

  <h2>{{ t("playground.account.title") }}</h2>
  <form v-if="play.tenantId.value === null" class="row" role="form" :aria-label="t('playground.account.formLabel')" @submit.prevent="create">
    <input v-model="name" :aria-label="t('playground.account.name')" :placeholder="t('playground.account.name')" required />
    <select v-model="currency" :aria-label="t('playground.account.currency')" required>
      <option value="" disabled>{{ t("playground.account.currencyPlaceholder") }}</option>
      <option v-for="c in CURRENCIES" :key="c" :value="c">{{ c }}</option>
    </select>
    <input v-model="timezone" class="wide" :aria-label="t('playground.account.timezone')" :placeholder="t('playground.account.timezonePlaceholder')" required />
    <input
      v-model="graceHours"
      type="number"
      min="0"
      max="168"
      :aria-label="t('playground.account.grace')"
      :placeholder="t('playground.account.gracePlaceholder')"
      required
    />
    <button type="submit" :disabled="play.busy.value">{{ t("playground.account.create") }}</button>
  </form>
  <i18n-t v-else keypath="playground.account.ready" tag="p" class="ok" scope="global">
    <template #tenant>
      <Link to="/tenants/$id" :params="{ id: play.tenantId.value }" class="mono">{{ play.tenantId.value }}</Link>
    </template>
    <template #key>
      <code>{{ play.key.value?.slice(0, 16) }}…</code>
    </template>
  </i18n-t>

  <h2>{{ t("playground.setup.title") }}</h2>
  <div class="row">
    <button type="button" :disabled="play.busy.value || play.key.value === null || play.ready.value" @click="play.setUp.mutate()">
      {{ t("playground.setup.button") }}
    </button>
    <label>
      {{ t("playground.setup.outcome") }}
      <select
        :aria-label="t('playground.setup.outcome')"
        :disabled="!play.ready.value || play.busy.value"
        :value="play.scenario.value"
        @change="play.chooseScenario.mutate(($event.target as HTMLSelectElement).value as Scenario)"
      >
        <option v-for="s in SCENARIOS" :key="s" :value="s">{{ t(`playground.scenarios.${s}`) }}</option>
      </select>
    </label>
  </div>

  <h2>{{ t("playground.usage.title") }}</h2>
  <div class="row">
    <input v-model="quantity" type="number" min="1" :aria-label="t('playground.usage.calls')" style="min-width: 120px" />
    <button type="button" :disabled="play.busy.value || !play.ready.value || play.closed.value" @click="play.sendUsage.mutate(Number(quantity))">
      {{ t("playground.usage.send") }}
    </button>
    <span class="muted" data-testid="sent">{{ t("playground.usage.sent", { n: new Intl.NumberFormat(locale).format(play.sent.value) }) }}</span>
  </div>

  <h2>{{ t("playground.close.title") }}</h2>
  <div class="row">
    <button type="button" :disabled="play.busy.value || !play.ready.value || play.sent.value === 0 || play.closed.value" @click="play.closePeriod.mutate()">
      {{ t("playground.close.button") }}
    </button>
    <span class="muted">{{ t("playground.close.hint") }}</span>
  </div>

  <template v-if="play.closed.value">
    <h2>{{ t("playground.invoice.title") }}</h2>
    <p v-if="(play.invoices.data.value ?? []).length === 0" class="muted">{{ t("playground.invoice.waiting") }}</p>
    <table>
      <tbody>
        <tr v-for="inv in play.invoices.data.value ?? []" :key="inv.id" data-testid="playground-invoice">
          <td>{{ inv.number }}</td>
          <td><span class="badge">{{ inv.saga_state }}</span></td>
          <td><span class="badge">{{ inv.status }}</span></td>
          <td class="num">{{ inv.total_minor }}</td>
          <td>
            <Link v-if="play.tenantId.value && inv.id" to="/tenants/$id/invoices/$invoiceId" :params="{ id: play.tenantId.value, invoiceId: inv.id }">
              {{ t("playground.invoice.open") }}
            </Link>
          </td>
        </tr>
      </tbody>
    </table>
  </template>

  <template v-if="play.log.value.length > 0">
    <h2>{{ t("playground.log.title") }}</h2>
    <ul>
      <li v-for="(line, i) in play.log.value" :key="i">{{ t(line.key, line.params ?? {}) }}</li>
    </ul>
  </template>
</template>
