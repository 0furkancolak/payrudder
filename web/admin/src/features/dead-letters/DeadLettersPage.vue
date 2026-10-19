<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { useDeadLetters, useRedrive } from "./api";

const { t } = useI18n();
const queue = ref("");
const list = useDeadLetters(queue);
const redrive = useRedrive();
const note = ref<{ queue: string; count: number } | null>(null);

async function run(name: string, count: number) {
  if (!window.confirm(t("deadLetters.confirm", { count, queue: name }))) return;
  note.value = null;
  const r = await redrive.mutateAsync(name).catch(() => null);
  if (r) note.value = { queue: name, count: Number(r.redriven ?? 0) };
}
</script>

<template>
  <h1>{{ t("deadLetters.title") }}</h1>
  <p class="muted">{{ t("deadLetters.intro", { pattern: "{queue}.dlq" }) }}</p>
  <p v-if="list.error.value" class="error">{{ toApiError(list.error.value).message }}</p>
  <p v-if="redrive.error.value" class="error">{{ toApiError(redrive.error.value).message }}</p>
  <p v-if="note" class="ok">{{ t("deadLetters.redriven", note) }}</p>
  <table>
    <thead>
      <tr>
        <th>{{ t("deadLetters.columns.queue") }}</th>
        <th class="num">{{ t("deadLetters.columns.messages") }}</th>
        <th />
      </tr>
    </thead>
    <tbody>
      <tr v-for="q in list.data.value?.queues ?? []" :key="q.queue" :data-testid="`dlq-${q.queue}`">
        <td>
          <button class="link" type="button" @click="queue = queue === q.queue ? '' : (q.queue ?? '')">{{ q.queue }}.dlq</button>
        </td>
        <td class="num" data-testid="dlq-count">{{ Number(q.messages ?? 0) }}</td>
        <td>
          <button class="danger" type="button" :disabled="Number(q.messages ?? 0) === 0" @click="run(q.queue ?? '', Number(q.messages ?? 0))">
            {{ t("deadLetters.redrive") }}
          </button>
        </td>
      </tr>
    </tbody>
  </table>

  <template v-if="queue">
    <h2>{{ t("deadLetters.messagesOf", { queue }) }}</h2>
    <table>
      <thead>
        <tr>
          <th>{{ t("deadLetters.messageColumns.id") }}</th>
          <th class="num">{{ t("deadLetters.messageColumns.deliveries") }}</th>
          <th>{{ t("deadLetters.messageColumns.reason") }}</th>
          <th class="num">{{ t("deadLetters.messageColumns.bytes") }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(m, i) in list.data.value?.messages ?? []" :key="`${m.message_id}-${i}`">
          <td class="mono">{{ m.message_id }}</td>
          <td class="num">{{ m.delivery_count }}</td>
          <td>{{ m.reason }}</td>
          <td class="num">{{ m.body_bytes }}</td>
        </tr>
      </tbody>
    </table>
  </template>
</template>
