<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AppField from "@/app/layout/AppField.vue";
import { useI18n } from "vue-i18n";
import CustomerSelect from "@/features/customers/CustomerSelect.vue";
import { useAttemptKey } from "@/shared/lib/key";
import { PURCHASE_QUANTITY_MAX } from "@/shared/lib/rules";
import { absoluteUrl } from "@/shared/lib/url";
import { Button } from "@/shared/ui/button";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useCreatePurchaseLink } from "./api";
import { validQuantity } from "./lib";

/** The link a customer opens to buy one pack. The URL is returned once, right after creation, and is not kept anywhere else. */
const props = defineProps<{ tenantId: string; pack: { id: string; name: string } | null }>();
const emit = defineEmits<{ close: [] }>();
const { t } = useI18n();

const customer = ref("");
const quantity = ref("1");
const promotion = ref("");
// One key per attempt: a retry of the same click finds the link made first, but a different customer, quantity or pack is a different request.
const attempt = useAttemptKey();
watch([customer, quantity, promotion, () => props.pack?.id], attempt.renew);
// The sheet is open while a pack is chosen; closing it (or choosing another pack) starts the form clean.
const open = computed({
  get: () => props.pack !== null,
  set: (v) => {
    if (!v) emit("close");
  },
});
const createLink = useCreatePurchaseLink(() => props.tenantId);
watch(
  () => props.pack?.id,
  () => {
    customer.value = promotion.value = "";
    quantity.value = "1";
    createLink.reset();
  },
);
const quantityValid = computed(() => validQuantity(quantity.value, PURCHASE_QUANTITY_MAX));
const created = computed(() => createLink.data.value);
const shownUrl = computed(() => absoluteUrl(created.value?.url, window.location.origin));

function submit() {
  if (!props.pack || !customer.value || !quantityValid.value) return;
  createLink.mutate({ packId: props.pack?.id ?? "", customer: customer.value, quantity: Number(quantity.value), key: attempt.key.value, promotion: promotion.value }, { onSuccess: attempt.renew });
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.credits.purchase.title") }}: {{ pack?.name }}</SheetTitle>
        <SheetDescription>{{ t("billing.credits.purchase.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="purchase-link-form" class="grid gap-4" role="form" :aria-label="t('billing.credits.purchase.formLabel')" @submit.prevent="submit">
          <AppField />
          <CustomerSelect v-model="customer" :tenant-id="tenantId" named required :label="t('billing.credits.purchase.customer')" :placeholder="t('billing.credits.purchase.pickCustomer')" />
          <Input v-model="quantity" inputmode="numeric" :aria-label="t('billing.credits.purchase.quantity')" :placeholder="t('billing.credits.purchase.quantity')" />
          <Input v-model="promotion" maxlength="40" :aria-label="t('billing.credits.purchase.promotionCode')" :placeholder="t('billing.credits.purchase.promotionCode')" :title="t('billing.paymentLinks.promotionHint')" />
          <ErrorText :error="createLink.error.value" />
        </form>
        <div v-if="created && createLink.isSuccess.value && shownUrl" class="border-warning/50 bg-warning/10 grid gap-2 rounded-md border p-3" role="status">
          <p class="text-sm font-medium">{{ t("billing.credits.purchase.shownOnce") }}</p>
          <CopyField :value="shownUrl" :label="t('billing.credits.purchase.url')" :copy-label="t('billing.credits.purchase.copy')" testid="purchase-link-url" />
        </div>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("billing.credits.purchase.close") }}</Button>
        <Button type="submit" form="purchase-link-form" :disabled="createLink.isPending.value || !customer || !quantityValid">{{ t("billing.credits.purchase.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
