<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import CustomerSelect from "@/features/customers/CustomerSelect.vue";
import { useFormat } from "@/shared/lib/format";
import { useAttemptKey } from "@/shared/lib/key";
import { CREDITS_RE } from "@/shared/lib/rules";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreditBalance, useGrantCredits } from "./api";
import { formatCredits } from "./lib";

/** One customer's credits: what can be spent now, the lots it comes from, and a form to add more (a promotion or a manual adjustment). */
const props = defineProps<{ tenantId: string }>();
const customer = defineModel<string>("customer", { required: true });
const { t, locale } = useI18n();
const fmt = useFormat();

const balance = useCreditBalance(() => props.tenantId, customer);
const grant = useGrantCredits(() => props.tenantId);
const grantCredits = ref("");
const grantSource = ref<"promo" | "manual">("promo");
const grantExpiry = ref("");
const grantReason = ref("");
// One key per grant attempt: a double click or a retry after a timeout adds the credits once; a new grant (after success) gets a new key.
const attempt = useAttemptKey();
const grantValid = computed(() => customer.value !== "" && CREDITS_RE.test(grantCredits.value.trim()));
const credits = (v: string | number | undefined) => formatCredits(v, locale.value);

function submitGrant() {
  if (!grantValid.value) return;
  grant.mutate(
    {
      customer: customer.value,
      credits: grantCredits.value.trim(),
      source: grantSource.value,
      // The end of the chosen day, in the operator's time zone.
      expireTime: grantExpiry.value ? new Date(`${grantExpiry.value}T23:59:59`).toISOString() : undefined,
      reason: grantReason.value.trim() || undefined,
      key: attempt.key.value,
    },
    {
      onSuccess: () => {
        grantCredits.value = grantExpiry.value = grantReason.value = "";
        attempt.renew();
      },
    },
  );
}
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.credits.customer.title") }}</h2>
    <CustomerSelect v-model="customer" :tenant-id="tenantId" named :label="t('billing.credits.customer.pick')" :placeholder="t('billing.credits.customer.pick')" />

    <template v-if="customer">
      <ErrorText :error="balance.error.value" />
      <div v-if="balance.data.value" class="flex flex-wrap items-baseline gap-x-4 gap-y-1" role="status" :aria-label="t('billing.credits.customer.balance')">
        <span class="text-3xl font-semibold tabular-nums">{{ credits(balance.data.value.balance) }}</span>
        <span class="text-muted-foreground text-sm">{{ t("billing.credits.customer.balance") }}</span>
        <span v-if="balance.data.value.next_expire_time" class="text-muted-foreground text-sm">
          {{ t("billing.credits.customer.nextExpiry", { when: fmt.date(balance.data.value.next_expire_time, "dateTime") }) }}
        </span>
      </div>

      <Table v-if="(balance.data.value?.lots ?? []).length">
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.credits.lots.source") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.credits.lots.remaining") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.credits.lots.granted") }}</TableHead>
            <TableHead>{{ t("billing.credits.lots.expires") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="l in balance.data.value?.lots ?? []" :key="l.id">
            <TableCell>{{ t(`billing.credits.source.${l.source}`) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ credits(l.remaining) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ credits(l.granted) }}</TableCell>
            <TableCell>{{ l.expire_time ? fmt.date(l.expire_time, "dateTime") : t("billing.credits.lots.never") }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>

      <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-5" role="form" :aria-label="t('billing.credits.grant.formLabel')" @submit.prevent="submitGrant">
        <Input v-model="grantCredits" inputmode="numeric" :aria-label="t('billing.credits.grant.credits')" :placeholder="t('billing.credits.grant.credits')" required />
        <NativeSelect v-model="grantSource" :aria-label="t('billing.credits.grant.source')">
          <option value="promo">{{ t("billing.credits.source.promo") }}</option>
          <option value="manual">{{ t("billing.credits.source.manual") }}</option>
        </NativeSelect>
        <Input v-model="grantExpiry" type="date" :aria-label="t('billing.credits.grant.expiry')" :title="t('billing.credits.grant.expiryHint')" />
        <Input v-model="grantReason" maxlength="200" :aria-label="t('billing.credits.grant.reason')" :placeholder="t('billing.credits.grant.reason')" />
        <Button type="submit" :disabled="grant.isPending.value || !grantValid">{{ t("billing.credits.grant.submit") }}</Button>
      </form>
      <ErrorText :error="grant.error.value" />
    </template>
  </Card>
</template>
