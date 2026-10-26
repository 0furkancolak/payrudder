<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { cn } from "@/shared/lib/utils";
import { initialsOf, type Customer } from "./lib";

/** A customer's initials in a circle, for the list and the head of their sheet. Decorative: the name is always written next to it. */
const props = defineProps<{ customer: Pick<Customer, "name" | "email" | "external_id">; size?: "sm" | "lg" }>();
const { locale } = useI18n();
const letters = computed(() => initialsOf(props.customer, locale.value));
</script>

<template>
  <span
    aria-hidden="true"
    :class="cn('bg-brand-soft text-brand-strong inline-flex shrink-0 items-center justify-center rounded-full font-semibold select-none', size === 'lg' ? 'size-14 text-lg' : 'size-9 text-xs')"
    >{{ letters }}</span
  >
</template>
