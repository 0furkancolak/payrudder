<script setup lang="ts">
/**
 * A whole HTML document shown as data: in a frame whose `sandbox` is empty (no script, no forms, no top navigation, no access to this page's origin), through
 * `srcdoc`, never as markup of this page. The stored page a customer accepted and the server's preview of a text both go through it: the first is the
 * seller's evidence and is not ours to trust, and a preview is escaped by the server, so this is the second lock on the same door.
 */
defineProps<{ doc: string; title: string }>();
</script>

<template>
  <iframe sandbox="" :srcdoc="doc" :title="title" referrerpolicy="no-referrer" class="block h-full min-h-48 w-full rounded-xl border bg-white" data-testid="sandboxed-html" />
</template>
