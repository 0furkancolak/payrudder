<script setup lang="ts">
import type { HTMLAttributes } from "vue";
import { useErrorText } from "@/shared/lib/errors";
import { cn } from "@/shared/lib/utils";

/**
 * The one way a page shows a failure. Give it the `error` of a query or a mutation and it shows the API's error in the reader's language (through
 * `useErrorText`, so a translated key is used and the server's English message is only the fallback), or give it a `message` the page worked out itself
 * (a form's own check). It draws nothing when there is neither. `class` adds to the red small text (the spacing of the card it sits in); `box` puts it in
 * the tinted box the product's forms use.
 */
const props = defineProps<{ error?: unknown; message?: string; box?: boolean; class?: HTMLAttributes["class"] }>();
const errorText = useErrorText();
</script>

<template>
  <p v-if="error || message" role="alert" :class="cn('text-destructive text-sm', box && 'bg-destructive-soft rounded-2xl px-4 py-3', props.class)">{{ error ? errorText(error) : message }}</p>
</template>
