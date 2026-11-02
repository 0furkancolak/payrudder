<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Switch } from "@/shared/ui/switch";
import { Textarea } from "@/shared/ui/textarea";
import { useBlockCustomer } from "./api";
import { BLOCK_CATEGORIES, type BlockCategory, type Customer } from "./lib";

/**
 * The form that blocks a customer: why (a category), the seller's own note, if the block should end by itself the day it ends, and whether their service is suspended
 * too (entitlement checks answer "not allowed"; usage is still counted). The model holds the customer being
 * blocked (null is closed). What the block does is said above the form; the history of blocks and lifts is on the customer's sheet.
 */
const props = defineProps<{ tenantId: string }>();
const target = defineModel<Customer | null>("customer", { default: null });
const { t } = useI18n();

const blocking = useBlockCustomer(() => props.tenantId);
const category = ref<BlockCategory | "">("");
const reason = ref("");
const until = ref("");
const suspend = ref(false);
const open = computed({ get: () => target.value !== null, set: (v) => !v && (target.value = null) });
watch(target, (c) => {
  if (!c) return;
  category.value = "";
  reason.value = "";
  until.value = "";
  suspend.value = false;
  blocking.block.reset();
});

/** The first day a block can end on: tomorrow, in the reader's day. */
const tomorrow = computed(() => {
  const d = new Date();
  d.setDate(d.getDate() + 1);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
});
/** The block ends when that day begins, in the reader's time zone: the customer can buy again on it. */
const untilIso = computed(() => (until.value ? new Date(`${until.value}T00:00:00`).toISOString() : undefined));

function save() {
  const c = target.value;
  if (!c) return;
  blocking.block.mutate(
    { externalId: c.external_id ?? "", reason: reason.value.trim(), category: category.value || undefined, until: untilIso.value, suspend: suspend.value },
    { onSuccess: () => (target.value = null) },
  );
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.customers.block.title", { name: target?.name || target?.external_id || "" }) }}</SheetTitle>
        <SheetDescription>{{ t("billing.customers.block.description") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="block-customer" class="grid gap-4" @submit.prevent="save">
          <FormField :label="t('billing.customers.block.category')" for="block-category" :hint="t('billing.customers.block.categoryHint')">
            <NativeSelect id="block-category" v-model="category" data-testid="block-category">
              <option value="">{{ t("billing.customers.block.categoryNone") }}</option>
              <option v-for="c in BLOCK_CATEGORIES" :key="c" :value="c">{{ t(`billing.customers.block.categories.${c}`) }}</option>
            </NativeSelect>
          </FormField>
          <FormField :label="t('billing.customers.block.note')" for="block-note" :hint="t('billing.customers.block.noteHint')">
            <Textarea id="block-note" v-model="reason" maxlength="500" data-testid="block-note" />
          </FormField>
          <FormField :label="t('billing.customers.block.until')" for="block-until" :hint="t('billing.customers.block.untilHint')">
            <Input id="block-until" v-model="until" type="date" :min="tomorrow" data-testid="block-until" />
          </FormField>
          <FormField :label="t('billing.customers.block.suspend')" for="block-suspend" :hint="t('billing.customers.block.suspendHint')">
            <Switch id="block-suspend" v-model="suspend" data-testid="block-suspend" />
          </FormField>
          <ErrorText :error="blocking.block.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="target = null">{{ t("billing.customers.address.cancel") }}</Button>
        <Button type="submit" form="block-customer" :disabled="blocking.block.isPending.value" data-testid="block-submit">{{ t("billing.customers.block.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
