<script setup lang="ts">
import { CreditCard, Loader2, Trash2 } from "@lucide/vue";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { Button } from "@/shared/ui/button";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useCustomerCards, useDeleteCustomerCard, type StoredCard } from "./api";

/**
 * The cards the payment provider holds for a customer (iyzico's card storage; never a number): the bank, the last four digits and the expiry, the one the customer's
 * invoices are charged with marked. Payrudder cannot add a card (it holds no card numbers; whoever does sets the payment method); it can delete one, after a question.
 * Deleting the card in use says so, since the next charge fails until a new payment method is set. `readonly` (the sheet opened over another one) shows the list only.
 */
const props = defineProps<{ tenantId: string; externalId: string; readonly?: boolean }>();
const { t } = useI18n();
const cards = useCustomerCards(() => props.tenantId, () => props.externalId);
const remove = useDeleteCustomerCard(() => props.tenantId, () => props.externalId);
const deleting = ref("");
const warning = ref("");

const expiry = (c: StoredCard) => (c.expire_month && c.expire_year ? `${c.expire_month}/${c.expire_year}` : "");
const name = (c: StoredCard) => [c.alias, c.bank].filter((x) => x !== "").join(" · ") || t("billing.customers.cards.card");

async function ask(c: StoredCard) {
  warning.value = "";
  const ok = await askConfirm({
    title: t("billing.customers.cards.deleteTitle", { last4: c.last4 }),
    body: c.in_use ? t("billing.customers.cards.deleteInUse") : t("billing.customers.cards.deleteBody"),
    confirmLabel: t("billing.customers.cards.delete"),
  });
  if (!ok) return;
  deleting.value = c.token;
  try {
    const done = await remove.mutateAsync(c.token);
    if (done.was_in_use) warning.value = t("billing.customers.cards.deletedInUse");
  } catch {
    // The mutation's error is shown under the list.
  } finally {
    deleting.value = "";
  }
}
</script>

<template>
  <section class="flex flex-col gap-2" :aria-label="t('billing.customers.cards.title')" data-testid="customer-cards">
    <h3 class="m-0 text-sm font-semibold">{{ t("billing.customers.cards.title") }}</h3>
    <ErrorState :error="cards.error.value" :retrying="cards.isFetching.value" compact @retry="cards.refetch()" />
    <Skeleton v-if="cards.isPending.value && !cards.error.value" class="h-10 rounded-2xl" />
    <p v-else-if="(cards.data.value?.cards ?? []).length === 0 && !cards.error.value" class="text-muted-foreground m-0 text-sm" data-testid="cards-empty">{{ t("billing.customers.cards.none") }}</p>
    <ul v-else class="divide-border/70 m-0 flex list-none flex-col divide-y p-0 text-sm">
      <li v-for="c in cards.data.value?.cards" :key="c.token" class="flex flex-wrap items-center justify-between gap-2 py-2.5" data-testid="customer-card">
        <span class="flex min-w-0 items-center gap-2">
          <CreditCard class="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
          <span class="flex min-w-0 flex-col">
            <span class="truncate font-medium">{{ name(c) }}</span>
            <span class="text-muted-foreground mono text-xs">{{ c.bin ? `${c.bin.slice(0, 6)}••` : "" }}•••• {{ c.last4 }}<template v-if="expiry(c)"> · {{ expiry(c) }}</template><template v-if="c.association"> · {{ c.association }}</template></span>
          </span>
          <StatusPill v-if="c.in_use" tone="info" data-testid="card-in-use">{{ t("billing.customers.cards.inUse") }}</StatusPill>
        </span>
        <Button v-if="!readonly" size="xs" variant="ghost" :disabled="deleting === c.token" :aria-label="t('billing.customers.cards.deleteAria', { last4: c.last4 })" data-testid="card-delete" @click="ask(c)">
          <Loader2 v-if="deleting === c.token" class="animate-spin" aria-hidden="true" /><Trash2 v-else aria-hidden="true" />
        </Button>
      </li>
    </ul>
    <ErrorText :error="remove.error.value" />
    <p v-if="warning" class="text-warning m-0 text-xs" role="status" data-testid="card-warning">{{ warning }}</p>
    <p class="text-muted-foreground m-0 text-xs">{{ t("billing.customers.cards.hint") }}</p>
  </section>
</template>
