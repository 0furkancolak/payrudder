<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { StatusPill, type Tone } from "@/shared/ui/status-pill";
import { Textarea } from "@/shared/ui/textarea";
import { useCustomerBlockAppeals, useDecideCustomerBlockAppeal } from "./api";

/**
 * The latest appeal of a blocked customer (plan 13), in their sheet. The statement is the customer's own words, so it is printed as text only (Vue escapes it, the box keeps
 * its line breaks) and labelled as untrusted. An open one can be accepted (the block is lifted, as the lift button does) or rejected with an answer, by a member who can
 * write: a viewer (or a peek over another sheet) reads it and sees no button, because the server would refuse them.
 */
const props = defineProps<{ tenantId: string; externalId: string; locked: boolean }>();
const { t } = useI18n();
const fmt = useFormat();
const list = useCustomerBlockAppeals(() => props.tenantId, () => props.externalId);
const latest = computed(() => list.data.value?.appeals?.[0]);
const decide = useDecideCustomerBlockAppeal(() => props.tenantId);
const answer = ref("");
const TONES: Record<string, Tone> = { open: "warning", accepted: "success", rejected: "danger", closed: "neutral" };

async function send(decision: "accepted" | "rejected") {
  const a = latest.value;
  if (!a) return;
  if (decision === "accepted" && !(await askConfirm({ body: t("billing.customers.appeal.confirmAccept", { name: props.externalId }) }))) return;
  decide.mutate({ externalId: props.externalId, id: a.id, decision, answer: answer.value.trim() }, { onSuccess: () => (answer.value = "") });
}
</script>

<template>
  <div v-if="latest" class="border-border flex flex-col gap-2 rounded-2xl border px-4 py-3 text-sm" data-testid="customer-appeal">
    <div class="flex flex-wrap items-center gap-2">
      <span class="font-medium">{{ t("billing.customers.appeal.title") }}</span>
      <StatusPill :tone="TONES[latest.status] ?? 'neutral'" data-testid="customer-appeal-status">{{ t(`billing.customers.appeal.status.${latest.status}`) }}</StatusPill>
      <span class="text-muted-foreground text-xs">{{ t("billing.customers.appeal.sent", { date: fmt.date(latest.create_time, "dateTime") }) }}</span>
    </div>
    <p class="text-muted-foreground m-0 text-xs">{{ t("billing.customers.appeal.untrusted") }}</p>
    <blockquote class="border-border m-0 border-s-2 ps-3 break-words whitespace-pre-wrap" dir="auto" data-testid="customer-appeal-statement">{{ latest.statement }}</blockquote>
    <p v-if="latest.answer" class="m-0 break-words" dir="auto" data-testid="customer-appeal-answer">
      <span class="text-muted-foreground">{{ t("billing.customers.appeal.answer") }}:</span> {{ latest.answer }}
      <span v-if="latest.decided_by" class="text-muted-foreground text-xs">({{ t("billing.customers.appeal.decidedBy", { actor: latest.decided_by }) }})</span>
    </p>
    <form v-if="latest.status === 'open' && !locked" class="grid gap-3" @submit.prevent>
      <FormField :label="t('billing.customers.appeal.answer')" for="customer-appeal-answer" :hint="t('billing.customers.appeal.answerHint')">
        <Textarea id="customer-appeal-answer" v-model="answer" maxlength="1000" data-testid="customer-appeal-answer-input" />
      </FormField>
      <ErrorText :error="decide.error.value" />
      <div class="flex flex-wrap gap-2">
        <Button type="button" size="sm" :disabled="decide.isPending.value" data-testid="customer-appeal-accept" @click="send('accepted')">{{ t("billing.customers.appeal.accept") }}</Button>
        <Button type="button" size="sm" variant="outline" :disabled="decide.isPending.value || answer.trim() === ''" data-testid="customer-appeal-reject" @click="send('rejected')">{{ t("billing.customers.appeal.reject") }}</Button>
      </div>
    </form>
  </div>
</template>
