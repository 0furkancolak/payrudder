<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useCreditNotes } from "@/features/creditnotes/api";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import RelatedSection from "./RelatedSection.vue";

/** The credit notes issued against one invoice. */
const props = defineProps<{ tenantId: string; invoiceId: string }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const notes = useCreditNotes(() => props.tenantId, () => props.invoiceId);
const rows = computed(() => notes.data.value?.credit_notes ?? []);
</script>

<template>
  <RelatedSection :title="t('related.creditNotes')" :empty="rows.length === 0">
    <li v-for="n in rows" :key="n.id" class="bg-muted/50 flex items-center justify-between gap-3 rounded-xl px-3 py-2 text-sm" data-testid="related-credit-note">
      <span class="flex flex-col">
        <span class="mono font-medium">{{ n.number }}</span>
        <span class="text-muted-foreground text-xs">{{ fmt.date(n.create_time, "day") }}</span>
      </span>
      <span class="tabular font-medium">{{ formatMinor(n.total_minor, n.currency, locale) }}</span>
    </li>
  </RelatedSection>
</template>
