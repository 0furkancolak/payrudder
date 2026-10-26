<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useSubscriptions } from "@/features/subscriptions/api";
import { enumWord, label } from "@/shared/lib/labels";
import { Badge } from "@/shared/ui/badge";
import RelatedRow from "./RelatedRow.vue";
import RelatedSection from "./RelatedSection.vue";

/** The subscriptions of one customer; each opens the subscription's sheet. */
const props = defineProps<{ tenantId: string; customer: string }>();
const { t, te } = useI18n();
const subs = useSubscriptions(() => props.tenantId);
const rows = computed(() => (subs.data.value?.subscriptions ?? []).filter((s) => s.customer_external_id === props.customer));
</script>

<template>
  <RelatedSection :title="t('related.subscriptions')" :empty="rows.length === 0">
    <RelatedRow v-for="s in rows" :id="s.id ?? ''" :key="s.id" :tenant-id="tenantId" kind="subscription">
      <span class="mono font-medium">{{ s.plan_code }} v{{ s.plan_version }}</span>
      <Badge variant="secondary">{{ label(t, te, "subscriptionState", enumWord(s.state, "SUBSCRIPTION_STATE_")) }}</Badge>
    </RelatedRow>
  </RelatedSection>
</template>
