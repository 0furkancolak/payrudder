<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Textarea } from "@/shared/ui/textarea";
import { useAppHold, useSubmitAppeal } from "./api";

/**
 * What the owners of a held app may know and do (plan 14): the date its data is kept until (a ban), and the appeal: a form for an owner, then "sent, no answer yet" or
 * what the operator decided and wrote. The operator's category and note of the hold are not here. A viewer or an administrator reads the same lines and has no form.
 */
const props = defineProps<{ id: string }>();
const { t } = useI18n();
const fmt = useFormat();
const hold = useAppHold(() => props.id);
const submit = useSubmitAppeal(() => props.id);
const writing = ref(false);
const statement = ref("");
const appeal = computed(() => hold.data.value?.appeal ?? null);

function send() {
  const text = statement.value.trim();
  if (text) submit.mutate(text, { onSuccess: () => ((writing.value = false), (statement.value = "")) });
}
</script>

<template>
  <div v-if="hold.data.value" class="flex flex-col gap-2 text-sm" data-testid="hold-panel">
    <p v-if="hold.data.value.retention_until" class="m-0" data-testid="hold-retention">{{ t("tenant.hold.retention", { date: fmt.date(hold.data.value.retention_until, "day") }) }}</p>
    <template v-if="appeal">
      <p v-if="appeal.status === 'open'" class="m-0" data-testid="hold-appeal-open">{{ t("tenant.hold.appeal.sent", { date: fmt.date(appeal.create_time, "day") }) }}</p>
      <template v-else>
        <p class="m-0" :data-testid="`hold-appeal-${appeal.status}`">{{ t(`tenant.hold.appeal.${appeal.status}`, { date: fmt.date(appeal.decide_time, "day") }) }}</p>
        <p v-if="appeal.answer" class="m-0" dir="auto" data-testid="hold-appeal-answer">{{ t("tenant.hold.appeal.answer", { answer: appeal.answer }) }}</p>
      </template>
    </template>
    <div v-if="hold.data.value.can_appeal">
      <Button v-if="!writing" type="button" size="sm" variant="outline" data-testid="hold-appeal-open-form" @click="writing = true">{{ t("tenant.hold.appeal.button") }}</Button>
      <form v-else class="grid max-w-xl gap-3" @submit.prevent="send">
        <FormField :label="t('tenant.hold.appeal.statement')" :for="`appeal-${id}`" :hint="t('tenant.hold.appeal.hint')">
          <Textarea :id="`appeal-${id}`" v-model="statement" maxlength="2000" data-testid="hold-appeal-statement" />
        </FormField>
        <ErrorText :error="submit.error.value" />
        <div class="flex gap-2">
          <Button type="submit" size="sm" :disabled="submit.isPending.value || statement.trim() === ''" data-testid="hold-appeal-submit">{{ t("tenant.hold.appeal.submit") }}</Button>
          <Button type="button" size="sm" variant="ghost" @click="writing = false">{{ t("tenant.hold.appeal.cancel") }}</Button>
        </div>
      </form>
    </div>
  </div>
</template>
