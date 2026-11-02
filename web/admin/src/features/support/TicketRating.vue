<script setup lang="ts">
import { Star } from "@lucide/vue";
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Textarea } from "@/shared/ui/textarea";
import { useMySupportActions, type SupportTicket } from "./api";

/**
 * How the person found the help on a solved or closed ticket: one to five stars and an optional comment, sent to the operator's report. A second rating replaces the
 * first; the person sees their own while the form is closed.
 */
const props = defineProps<{ ticket: Pick<SupportTicket, "id" | "rating" | "rating_comment"> }>();
const { t } = useI18n();
const actions = useMySupportActions();
const editing = ref(props.ticket.rating === 0);
const stars = ref(props.ticket.rating);
const comment = ref(props.ticket.rating_comment);
watch(() => props.ticket.id, () => {
  editing.value = props.ticket.rating === 0;
  stars.value = props.ticket.rating;
  comment.value = props.ticket.rating_comment;
  actions.rate.reset();
});
function send() {
  if (stars.value < 1) return;
  actions.rate.mutate({ id: props.ticket.id, rating: stars.value, comment: comment.value.trim() }, { onSuccess: () => (editing.value = false) });
}
</script>

<template>
  <section class="bg-muted flex flex-col gap-2 rounded-2xl px-4 py-3 text-sm" data-testid="ticket-rating">
    <template v-if="!editing && ticket.rating > 0">
      <p class="m-0 flex flex-wrap items-center gap-2">
        <span class="font-medium">{{ t("support.rating.yours") }}</span>
        <span class="inline-flex" role="img" :aria-label="t('support.rating.stars', { n: ticket.rating })" data-testid="rating-shown"><Star v-for="n in 5" :key="n" class="size-4" :class="n <= ticket.rating ? 'fill-current' : 'text-muted-foreground'" aria-hidden="true" /></span>
        <Button type="button" variant="ghost" size="sm" data-testid="rating-change" @click="editing = true">{{ t("support.rating.change") }}</Button>
      </p>
      <p v-if="ticket.rating_comment" class="m-0 break-words whitespace-pre-wrap" dir="auto">{{ ticket.rating_comment }}</p>
    </template>
    <form v-else class="grid gap-2" @submit.prevent="send">
      <p class="m-0 font-medium">{{ t("support.rating.ask") }}</p>
      <div class="flex gap-1" role="group" :aria-label="t('support.rating.ask')">
        <button v-for="n in 5" :key="n" type="button" class="rounded-full p-1" :aria-label="t('support.rating.stars', { n })" :aria-pressed="stars === n" :data-testid="`rating-star-${n}`" @click="stars = n">
          <Star class="size-6" :class="n <= stars ? 'fill-current' : 'text-muted-foreground'" aria-hidden="true" />
        </button>
      </div>
      <Textarea v-model="comment" rows="2" maxlength="1000" :aria-label="t('support.rating.comment')" :placeholder="t('support.rating.comment')" data-testid="rating-comment" />
      <ErrorText :error="actions.rate.error.value" />
      <div><Button type="submit" size="sm" :disabled="stars < 1 || actions.rate.isPending.value" data-testid="rating-send">{{ t("support.rating.send") }}</Button></div>
    </form>
  </section>
</template>
