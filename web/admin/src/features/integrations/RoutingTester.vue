<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { readOf, together } from "@/shared/lib/reads";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Button } from "@/shared/ui/button";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { splitAccountKey } from "@/features/providers/connections";
import { LANGUAGE_NAMES } from "@/features/contracts/lib";
import { NativeSelect } from "@/shared/ui/native-select";
import { MAIL_LANGUAGES, eInvoiceAccounts, eInvoiceTaker, mailAccounts, mailTaker, type MailTaker, type Taker } from "./accounts";
import { useEInvoiceIntegration, useEInvoiceRouting, useMailIntegration, useMailRouting, usePreviewLiveRouting } from "./api";
import { BRANDS, type IntegrationId } from "./brands";

/**
 * "Who takes this customer?": for a customer in a country paying in a currency, which payment account charges them (the server's own matching, which also
 * knows what each provider can charge), which e-invoice account issues their documents (the same rule as the worker's: the account a rule names for the
 * country, else the default) and which sender mails them, in the language chosen (the account a rule names for it, else the default). Nothing is created.
 */
const props = defineProps<{ appId: string }>();
const { t } = useI18n();
const preview = usePreviewLiveRouting(() => props.appId);
const eInvoice = useEInvoiceIntegration(() => props.appId);
const eRules = useEInvoiceRouting(() => props.appId);
const mail = useMailIntegration(() => props.appId);
const mRules = useMailRouting(() => props.appId);
// The answer is worked out from these four: with one missing, "the built-in sender" or "no account" would be a guess, so the question waits until all have been read.
const rulesRead = computed(() => together([readOf(eInvoice), readOf(eRules), readOf(mail), readOf(mRules)]));

const country = ref("");
const currency = ref("");
const language = ref("en");
/** The question that was asked, so the answer stays about it while the fields are edited. */
const asked = ref<{ country: string; language: string } | null>(null);

const accounts = computed(() => eInvoiceAccounts(eInvoice.data.value));
const taker = computed<Taker | null>(() => (asked.value ? eInvoiceTaker(accounts.value, eRules.data.value ?? [], asked.value.country) : null));
const mailWho = computed<MailTaker | null>(() => (asked.value ? mailTaker(mailAccounts(mail.data.value), mRules.data.value ?? [], asked.value.language) : null));
const nameOf = (key: string) => {
  const { provider, label } = splitAccountKey(key);
  const base = BRANDS[provider as IntegrationId]?.name ?? t(`product.goLive.provider.names.${provider}`, t(`product.integrations.names.${provider}`));
  return label ? `${base} (${label})` : base;
};

function run() {
  const c = country.value.trim().toUpperCase();
  asked.value = { country: c, language: language.value };
  preview.mutate({ country: c, currency: currency.value });
}
</script>

<template>
  <section class="surface flex flex-col gap-4 p-5" data-testid="routing-tester">
    <div>
      <h2 class="text-base font-semibold tracking-tight">{{ t("product.integrations.accounts.tester.title") }}</h2>
      <p class="text-muted-foreground max-w-2xl text-sm">{{ t("product.integrations.accounts.tester.intro") }}</p>
    </div>
    <ErrorState v-if="rulesRead.error" :error="rulesRead.error" :retrying="rulesRead.fetching" compact class="mx-0" @retry="rulesRead.retry()" />
    <form class="flex flex-wrap items-end gap-3" role="form" :aria-label="t('product.integrations.accounts.tester.title')" @submit.prevent="run">
      <label class="grid gap-1 text-xs">{{ t("product.providers.account.routing.country") }}
        <Input v-model="country" class="mono w-24 uppercase" maxlength="2" :placeholder="t('product.providers.account.routing.any')" data-testid="tester-country" />
      </label>
      <label class="grid gap-1 text-xs">{{ t("product.providers.account.routing.currency") }}
        <CurrencySelect v-model="currency" class="w-40" :aria-label="t('product.providers.account.routing.currency')" :blank="t('product.providers.account.routing.any')" />
      </label>
      <label class="grid gap-1 text-xs">{{ t("product.integrations.accounts.mRouting.mailLanguage") }}
        <NativeSelect v-model="language" class="w-36" data-testid="tester-language">
          <option v-for="l in MAIL_LANGUAGES" :key="l" :value="l">{{ LANGUAGE_NAMES[l] }}</option>
        </NativeSelect>
      </label>
      <Button type="submit" variant="outline" :disabled="preview.isPending.value || rulesRead.loading || !!rulesRead.error" data-testid="tester-run">{{ t("product.integrations.accounts.tester.run") }}</Button>
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
        <dd>
          <template v-if="mailWho?.via === 'rule'">{{ t("product.integrations.accounts.tester.mailByRule", { account: nameOf(mailWho.account ?? ""), language: LANGUAGE_NAMES[asked.language as keyof typeof LANGUAGE_NAMES] ?? asked.language }) }}</template>
          <template v-else-if="mailWho?.via === 'default'">{{ t("product.integrations.accounts.tester.mailDefault", { account: nameOf(mailWho.account ?? "") }) }}</template>
          <template v-else>{{ t("product.integrations.accounts.tester.mailBuiltIn") }}</template>
        </dd>
      </div>
    </dl>
  </section>
</template>
