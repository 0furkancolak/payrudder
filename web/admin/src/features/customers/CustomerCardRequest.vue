<script setup lang="ts">
import { CreditCard } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { sheetName } from "@/shared/lib/sheets";
import { Button } from "@/shared/ui/button";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useAskForCard } from "./api";
import { returnUrlProblem } from "./lib";

/**
 * "Ask for a card": makes the link to Stripe's own page where this customer saves a card for later charges, to be sent to them. Nothing is charged and no card number passes
 * through Payrudder; the card becomes the customer's payment method when Stripe says it was saved. Payrudder collects and keeps no consent in this flow, and the sheet says so:
 * the seller sends the link with their own terms and keeps the agreement (Stripe's guide asks for it). Offered only to somebody who may write, like the other actions.
 */
const props = defineProps<{ tenantId: string; customer: string }>();
const { t } = useI18n();
const open = ref(false);
const returnUrl = ref("");
const touched = ref(false);
const ask = useAskForCard(() => props.tenantId, () => props.customer);
const problem = computed(() => returnUrlProblem(returnUrl.value));
const link = computed(() => ask.data.value?.url ?? "");
watch(open, (isOpen) => {
  if (!isOpen) return;
  touched.value = false;
  ask.reset();
});
watch(
  () => props.customer,
  () => ask.reset(),
);

function make() {
  touched.value = true;
  if (problem.value) return;
  ask.mutate(returnUrl.value.trim());
}
</script>

<template>
  <Button size="sm" variant="outline" data-testid="customer-ask-card" @click="open = true"><CreditCard aria-hidden="true" />{{ t("billing.customers.askCard.action") }}</Button>
  <Sheet v-model:open="open" :name="sheetName('ask-card', customer)">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.customers.askCard.title") }}</SheetTitle>
        <SheetDescription>{{ t("billing.customers.askCard.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="ask-card" class="grid gap-4" @submit.prevent="make">
          <p class="bg-muted/60 text-foreground m-0 rounded-xl p-3 text-sm" role="note" data-testid="ask-card-consent">{{ t("billing.customers.askCard.consent") }}</p>
          <div class="grid gap-1.5">
            <Label for="ask-card-return">{{ t("billing.customers.askCard.returnUrl") }}</Label>
            <Input id="ask-card-return" v-model="returnUrl" type="url" inputmode="url" dir="ltr" placeholder="https://" maxlength="2000" autocomplete="off" :aria-invalid="touched && !!problem" data-testid="ask-card-return" />
            <p class="text-muted-foreground m-0 text-xs">{{ t("billing.customers.askCard.returnUrlHint") }}</p>
            <p v-if="touched && problem" class="text-destructive m-0 text-xs" role="alert" data-testid="ask-card-problem">{{ t(`billing.customers.askCard.errors.${problem}`) }}</p>
          </div>
          <ErrorText :error="ask.error.value" />
          <div v-if="link" class="grid gap-2" data-testid="ask-card-result">
            <p class="m-0 text-sm font-medium">{{ t("billing.customers.askCard.made") }}</p>
            <CopyField :value="link" :label="t('billing.customers.askCard.link')" :copy-label="t('billing.customers.askCard.copy')" testid="ask-card-link" />
            <p class="text-muted-foreground m-0 text-xs">{{ t("billing.customers.askCard.after") }}</p>
          </div>
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="outline" @click="open = false">{{ t("billing.customers.askCard.close") }}</Button>
        <Button type="submit" form="ask-card" :disabled="ask.isPending.value" data-testid="ask-card-make">{{ link ? t("billing.customers.askCard.again") : t("billing.customers.askCard.make") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
