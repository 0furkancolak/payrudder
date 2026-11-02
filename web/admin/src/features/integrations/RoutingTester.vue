<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { splitAccountKey } from "@/features/providers/connections";
import { eInvoiceAccounts, eInvoiceTaker, type Taker } from "./accounts";
import { useEInvoiceIntegration, useEInvoiceRouting, useMailIntegration, usePreviewLiveRouting } from "./api";
import { BRANDS, type IntegrationId } from "./brands";

/**
 * "Who takes this customer?": for a customer in a country paying in a currency, which payment account charges them (the server's own matching, which also
 * knows what each provider can charge), which e-invoice account issues their documents (the same rule as the worker's: the account a rule names for the
 * country, else the default) and which sender mails them. Nothing is created.
 */
const props = defineProps<{ appId: string }>();
const { t } = useI18n();
const preview = usePreviewLiveRouting(() => props.appId);
const eInvoice = useEInvoiceIntegration(() => props.appId);
const eRules = useEInvoiceRouting(() => props.appId);
const mail = useMailIntegration(() => props.appId);

const country = ref("");
const currency = ref("");
/** The question that was asked, so the answer stays about it while the fields are edited. */
const asked = ref<{ country: string } | null>(null);

const accounts = computed(() => eInvoiceAccounts(eInvoice.data.value));
const taker = computed<Taker | null>(() => (asked.value ? eInvoiceTaker(accounts.value, eRules.data.value ?? [], asked.value.country) : null));
const nameOf = (key: string) => {
  const { provider, label } = splitAccountKey(key);
  const base = BRANDS[provider as IntegrationId]?.name ?? t(`product.goLive.provider.names.${provider}`, t(`product.integrations.names.${provider}`));
  return label ? `${base} (${label})` : base;
};

function run() {
  const c = country.value.trim().toUpperCase();
  asked.value = { country: c };
  preview.mutate({ country: c, currency: currency.value });
}
</script>

<template>
  <section class="surface flex flex-col gap-4 p-5" data-testid="routing-tester">
    <div>
      <h2 class="text-base font-semibold tracking-tight">{{ t("product.integrations.accounts.tester.title") }}</h2>
      <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.integrations.accounts.tester.intro") }}</p>
    </div>
    <form class="flex flex-wrap items-end gap-3" role="form" :aria-label="t('product.integrations.accounts.tester.title')" @submit.prevent="run">
      <label class="grid gap-1 text-xs">{{ t("product.providers.account.routing.country") }}
        <Input v-model="country" class="mono w-24 uppercase" maxlength="2" :placeholder="t('product.providers.account.routing.any')" data-testid="tester-country" />
      </label>
      <label class="grid gap-1 text-xs">{{ t("product.providers.account.routing.currency") }}
        <CurrencySelect v-model="currency" class="w-40" :aria-label="t('product.providers.account.routing.currency')" :blank="t('product.providers.account.routing.any')" />
      </label>
      <Button type="submit" variant="outline" :disabled="preview.isPending.value" data-testid="tester-run">{{ t("product.integrations.accounts.tester.run") }}</Button>
    </form>
    <ErrorText :error="preview.error.value" />
    <dl v-if="asked && preview.data.value" class="grid gap-2 text-sm" data-testid="tester-result">
      <div class="flex flex-wrap gap-x-3" data-testid="tester-result-payment">
        <dt class="text-muted-foreground w-28 shrink-0">{{ t("product.integrations.accounts.kinds.payment") }}</dt>
        <dd>
          <template v-if="preview.data.value.provider">
            {{ t("product.integrations.accounts.tester.byRule", { account: nameOf(preview.data.value.provider), n: (preview.data.value.rule_index ?? 0) + 1 }) }}
            <span v-if="preview.data.value.can_charge === false" class="text-destructive"> {{ t("billing.providers.cannotCharge") }}</span>
          </template>
          <template v-else>{{ t("product.integrations.accounts.tester.byDefault") }}</template>
        </dd>
      </div>
      <div class="flex flex-wrap gap-x-3" data-testid="tester-result-einvoice">
        <dt class="text-muted-foreground w-28 shrink-0">{{ t("product.integrations.accounts.kinds.einvoice") }}</dt>
        <dd>
          <template v-if="taker?.via === 'rule'">{{ t("product.integrations.accounts.tester.byRuleCountry", { account: nameOf(taker.account ?? ""), country: asked.country }) }}</template>
          <template v-else-if="taker?.via === 'default'">{{ t("product.integrations.accounts.tester.eDefault", { account: nameOf(taker.account ?? "") }) }}</template>
          <template v-else>{{ t("product.integrations.accounts.tester.eNone") }}</template>
        </dd>
      </div>
      <div class="flex flex-wrap gap-x-3" data-testid="tester-result-mail">
        <dt class="text-muted-foreground w-28 shrink-0">{{ t("product.integrations.accounts.kinds.mail") }}</dt>
        <dd>{{ mail.data.value?.connected ? t("product.integrations.accounts.tester.mailOwn") : t("product.integrations.accounts.tester.mailBuiltIn") }}</dd>
      </div>
    </dl>
  </section>
</template>
