<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { programTerms, type MyProgram } from "./logic";

/** The seller's rules in words: what the partner earns, for how long, and when it can be paid. */
const props = defineProps<{ program: MyProgram | undefined }>();
const { t } = useI18n();
const fmt = useFormat();
const lines = computed(() => programTerms(props.program, (minor, currency) => fmt.amount(minor, currency)));
</script>

<template>
  <ul class="m-0 flex list-none flex-col gap-1.5 p-0 text-sm" data-testid="terms">
    <li v-for="l in lines" :key="l.key" :data-term="l.key">{{ t(`partner.terms.${l.key}`, l.params) }}</li>
  </ul>
</template>
