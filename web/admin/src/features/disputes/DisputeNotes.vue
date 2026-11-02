<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { Textarea } from "@/shared/ui/textarea";
import { useDisputeActions, useDisputeHistory } from "./api";
import type { Dispute } from "./dispute";

/**
 * What the seller wrote on a case and did with it, inside Payrudder: the notes and the marks that it was handled (newest first), a form for a note and the button that marks
 * the case handled or lifts the mark. Answering the case is done at the provider (Payrudder submits no evidence); this is where the seller keeps track of having done it. A
 * viewer reads and is given no form and no button.
 */
const props = defineProps<{ tenantId: string; dispute: Dispute }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
const history = useDisputeHistory(() => props.tenantId, () => props.dispute.id);
const actions = useDisputeActions(() => props.tenantId, () => props.dispute.id);
const events = computed(() => history.data.value?.events ?? []);
const note = ref("");
const error = computed(() => actions.addNote.error.value ?? actions.setHandled.error.value);

async function add() {
  const text = note.value.trim();
  if (!text) return;
  try {
    await actions.addNote.mutateAsync(text);
    note.value = "";
  } catch {
    // The mutation's error is shown under the form.
  }
}
</script>

<template>
  <section class="flex flex-col gap-3 border-t pt-3" :aria-label="t('disputes.notes.title')" data-testid="dispute-notes">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="m-0 text-sm font-semibold">{{ t("disputes.notes.title") }}</h3>
      <Button v-if="canWrite" type="button" size="sm" variant="outline" :disabled="actions.setHandled.isPending.value" data-testid="dispute-handled" @click="actions.setHandled.mutate(!dispute.handled)">
        {{ dispute.handled ? t("disputes.notes.lift") : t("disputes.notes.mark") }}
      </Button>
    </div>
    <p class="text-muted-foreground m-0 text-xs">{{ t("disputes.notes.hint") }}</p>
    <ErrorState :error="history.error.value" :retrying="history.isFetching.value" compact @retry="history.refetch()" />
    <ul v-if="events.length > 0" class="m-0 flex list-none flex-col gap-2 p-0" data-testid="dispute-events">
      <li v-for="e in events" :key="e.id" class="flex flex-col gap-0.5 text-sm" :data-action="e.action">
        <span v-if="e.action === 'note'" class="break-words whitespace-pre-wrap">{{ e.note }}</span>
        <span v-else class="font-medium">{{ t(`disputes.notes.event.${e.action}`) }}</span>
        <span class="text-muted-foreground text-xs break-words">{{ fmt.date(e.create_time, "dateTime") }}<template v-if="e.actor"> · {{ e.actor }}</template></span>
      </li>
    </ul>
    <p v-else-if="!history.isPending.value && !history.error.value" class="text-muted-foreground m-0 text-sm" data-testid="dispute-events-empty">{{ t("disputes.notes.empty") }}</p>
    <form v-if="canWrite" class="flex flex-col gap-2" @submit.prevent="add">
      <Textarea v-model="note" rows="2" maxlength="2000" :placeholder="t('disputes.notes.placeholder')" :aria-label="t('disputes.notes.placeholder')" data-testid="dispute-note-input" />
      <div><Button type="submit" size="sm" :disabled="actions.addNote.isPending.value || note.trim() === ''" data-testid="dispute-note-add">{{ t("disputes.notes.add") }}</Button></div>
    </form>
    <ErrorText :error="error" />
  </section>
</template>
