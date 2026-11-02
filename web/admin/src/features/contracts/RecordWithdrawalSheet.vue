<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { sheetName } from "@/shared/lib/sheets";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Textarea } from "@/shared/ui/textarea";
import { useRecordWithdrawal } from "./api";

/**
 * Records that a customer withdrew from a contract by a way Payrudder did not see (a mail, a call): what they said and, if it reached the seller earlier
 * than now, when. The seller decides it afterwards on the Contracts page. The request is sent with the source "console".
 */
const props = defineProps<{ tenantId: string; customer: string }>();
const open = defineModel<boolean>("open", { default: false });
const { t } = useI18n();

const record = useRecordWithdrawal(() => props.tenantId);
const statement = ref("");
const received = ref("");
watch(open, (v) => {
  if (!v) return;
  statement.value = received.value = "";
  record.reset();
});

/** The latest moment the field accepts: now, in the reader's time zone (the API refuses a time in the future). */
function latest(): string {
  const d = new Date();
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
}
/** The moment the statement reached the seller, in the reader's time zone, as the API takes it; empty is now. */
const receiveTime = () => (received.value ? new Date(received.value).toISOString() : undefined);
function submit() {
  if (statement.value.trim() === "") return;
  record.mutate({ customer: props.customer, statement: statement.value.trim(), receiveTime: receiveTime() }, { onSuccess: () => (open.value = false) });
}
</script>

<template>
  <Sheet v-model:open="open" :name="customer ? sheetName('record-withdrawal', customer) : undefined">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("contracts.record.title") }}</SheetTitle>
        <SheetDescription>{{ t("contracts.record.description", { customer }) }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="record-withdrawal" class="grid gap-4" role="form" novalidate :aria-label="t('contracts.record.formLabel')" @submit.prevent="submit">
          <FormField :label="t('contracts.record.statement')" for="withdrawal-statement" :hint="t('contracts.record.statementHint')">
            <Textarea id="withdrawal-statement" v-model="statement" maxlength="2000" class="min-h-32" dir="auto" data-testid="withdrawal-statement" />
          </FormField>
          <FormField :label="t('contracts.record.received')" for="withdrawal-received" :hint="t('contracts.record.receivedHint')">
            <Input id="withdrawal-received" v-model="received" type="datetime-local" :max="latest()" data-testid="withdrawal-received" />
          </FormField>
          <ErrorText :error="record.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="record-withdrawal" :disabled="record.isPending.value || statement.trim() === ''" data-testid="withdrawal-record-submit">{{ t("contracts.record.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
