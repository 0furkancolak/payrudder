<script setup lang="ts">
import { peek } from "./peek";

/**
 * A mention of an invoice that opens the invoice's sheet over the sheet or page it is on, instead of leaving for the invoices list. The text is the slot
 * (the number, usually); `class` styles the button like the link it replaces.
 */
defineProps<{ tenantId: string; id: string }>();
</script>

<template>
  <button type="button" class="cursor-pointer text-start" @click.stop="peek('invoice', tenantId, id)"><slot /></button>
</template>
