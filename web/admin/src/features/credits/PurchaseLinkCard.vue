<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import CustomerSelect from "@/features/customers/CustomerSelect.vue";
import { useAttemptKey } from "@/shared/lib/key";
import { PURCHASE_QUANTITY_MAX } from "@/shared/lib/rules";
import { absoluteUrl } from "@/shared/lib/url";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { useCreatePurchaseLink } from "./api";
import { validQuantity } from "./lib";

/** The link a customer opens to buy one pack. The URL is returned once, right after creation, and is not kept anywhere else. */
const props = defineProps<{ tenantId: string; pack: { id: string; name: string } }>();
const emit = defineEmits<{ close: [] }>();
const { t } = useI18n();

const customer = ref("");
const quantity = ref("1");
// One key per attempt: a retry of the same click finds the link made first, but a different customer, quantity or pack is a different request.
const attempt = useAttemptKey();
watch([customer, quantity, () => props.pack.id], attempt.renew);
const createLink = useCreatePurchaseLink(() => props.tenantId);
watch(
  () => props.pack.id,
  () => createLink.reset(),
);
const quantityValid = computed(() => validQuantity(quantity.value, PURCHASE_QUANTITY_MAX));
const created = computed(() => createLink.data.value as { url?: string; replayed?: boolean } | undefined);
const shownUrl = computed(() => absoluteUrl(created.value?.url, window.location.origin));

function submit() {
  if (!customer.value || !quantityValid.value) return;
  createLink.mutate({ packId: props.pack.id, customer: customer.value, quantity: Number(quantity.value), key: attempt.key.value }, { onSuccess: attempt.renew });
}
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.credits.purchase.title") }}: {{ pack.name }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.credits.purchase.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" role="form" :aria-label="t('billing.credits.purchase.formLabel')" @submit.prevent="submit">
      <CustomerSelect v-model="customer" :tenant-id="tenantId" named required :label="t('billing.credits.purchase.customer')" :placeholder="t('billing.credits.purchase.pickCustomer')" />
      <Input v-model="quantity" inputmode="numeric" :aria-label="t('billing.credits.purchase.quantity')" :placeholder="t('billing.credits.purchase.quantity')" />
      <Button type="submit" :disabled="createLink.isPending.value || !customer || !quantityValid">{{ t("billing.credits.purchase.create") }}</Button>
      <Button type="button" variant="outline" @click="emit('close')">{{ t("billing.credits.purchase.close") }}</Button>
    </form>
    <ErrorText :error="createLink.error.value" />
    <div v-if="created && createLink.isSuccess.value && shownUrl" class="border-warning/50 bg-warning/10 grid gap-2 rounded-md border p-3" role="status">
      <p class="text-sm font-medium">{{ t("billing.credits.purchase.shownOnce") }}</p>
      <CopyField :value="shownUrl" :label="t('billing.credits.purchase.url')" :copy-label="t('billing.credits.purchase.copy')" testid="purchase-link-url" />
    </div>
  </Card>
</template>
