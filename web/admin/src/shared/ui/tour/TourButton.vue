<script setup lang="ts">
import { Compass } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { startTour, tourSeen, type Tour } from "./tour";

/**
 * The tour icon of a page header: starts the page's tour. A small dot says the person has not taken it yet. It is drawn like the other header buttons
 * (theme, privacy), so it sits with them or in a page's own header.
 */
const props = defineProps<{ tour: Tour }>();
const { t } = useI18n();
</script>

<template>
  <button
    type="button"
    data-testid="tour-button"
    :data-tour-id="props.tour.id"
    :aria-label="t('tour.take')"
    :title="t('tour.take')"
    class="bg-card text-muted-foreground hover:text-foreground border-border/60 shadow-card focus-visible:ring-ring/50 relative inline-flex size-10 shrink-0 items-center justify-center rounded-xl border transition-colors outline-none focus-visible:ring-3"
    @click="startTour(props.tour)"
  >
    <Compass class="size-4" aria-hidden="true" />
    <span v-if="!tourSeen(props.tour.id)" class="bg-brand ring-card absolute end-1.5 top-1.5 size-2 rounded-full ring-2" aria-hidden="true" data-testid="tour-unseen"></span>
  </button>
</template>
