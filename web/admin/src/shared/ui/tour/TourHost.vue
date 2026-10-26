<script setup lang="ts">
import { useRouterState } from "@tanstack/vue-router";
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { activeStep, activeTour, endTour, nextStep, placeCard, prevStep, resolveTarget, skipStep, type Box } from "./tour";

/**
 * Draws the running tour over the page: the page dimmed, the step's element lit, and a card with the step's words and the buttons. Mounted once, by the
 * frame of the app. Keyboard: arrows (or Enter on a button) move, Escape skips, Tab stays inside the card. A step whose element is not on this screen is a
 * card in the middle, or is passed over when it is optional.
 */
const { t, locale } = useI18n();
const rtl = computed(() => (locale.value === "ar" ? true : document.documentElement.dir === "rtl"));
const route = useRouterState({ select: (s) => s.location.pathname });

const card = ref<HTMLElement | null>(null);
const box = ref<Box | null>(null);
const pos = ref({ x: 0, y: 0 });
const view = ref({ w: window.innerWidth, h: window.innerHeight });

const total = computed(() => activeTour.value?.tour.steps.length ?? 0);
const index = computed(() => activeTour.value?.index ?? 0);
const last = computed(() => index.value === total.value - 1);

let timer: ReturnType<typeof setInterval> | undefined;
let waited = 0;
let frame = 0;

/** Finds the element of the step and the card's place; called again as the page moves under the tour. */
function measure(): void {
  const step = activeStep.value;
  view.value = { w: window.innerWidth, h: window.innerHeight };
  const el = resolveTarget(step?.target);
  if (el) {
    const r = el.getBoundingClientRect();
    const pad = 6;
    box.value = { x: r.left - pad, y: r.top - pad, w: r.width + pad * 2, h: r.height + pad * 2 };
  } else {
    box.value = null;
  }
  const c = card.value?.getBoundingClientRect();
  pos.value = placeCard(box.value, { w: c?.width ?? 340, h: c?.height ?? 200 }, view.value);
}

function schedule(): void {
  cancelAnimationFrame(frame);
  frame = requestAnimationFrame(measure);
}

function stop(): void {
  clearInterval(timer);
  timer = undefined;
  window.removeEventListener("resize", schedule);
  window.removeEventListener("scroll", schedule, true);
}

// A step starts: look for its element (the page may still be drawing it), bring it into view and light it. An optional step that never shows is passed over.
watch(
  activeStep,
  async (step) => {
    stop();
    if (!step) return;
    waited = 0;
    window.addEventListener("resize", schedule);
    window.addEventListener("scroll", schedule, true);
    const found = resolveTarget(step.target);
    found?.scrollIntoView({ block: "center", inline: "nearest" });
    await nextTick();
    measure();
    card.value?.querySelector<HTMLElement>("[data-tour-primary]")?.focus({ preventScroll: true });
    timer = setInterval(() => {
      waited += 150;
      const now = resolveTarget(step.target);
      if (!now && step.optional && waited >= 450) return skipStep();
      if (now && waited <= 600) now.scrollIntoView({ block: "center", inline: "nearest" });
      measure();
    }, 150);
  },
  { immediate: true },
);

// Leaving the page ends the tour without counting it as seen: it was not finished.
watch(route, () => {
  if (activeTour.value) endTour(false);
});

function onKey(e: KeyboardEvent): void {
  if (!activeTour.value) return;
  const forward = rtl.value ? "ArrowLeft" : "ArrowRight";
  const back = rtl.value ? "ArrowRight" : "ArrowLeft";
  if (e.key === "Escape") {
    e.preventDefault();
    e.stopPropagation();
    endTour();
  } else if (e.key === forward) {
    e.preventDefault();
    nextStep();
  } else if (e.key === back) {
    e.preventDefault();
    prevStep();
  } else if (e.key === "Tab" && card.value) {
    const items = [...card.value.querySelectorAll<HTMLElement>("button:not([disabled])")];
    const first = items[0];
    const lastItem = items[items.length - 1];
    if (!first || !lastItem) return;
    if (!card.value.contains(document.activeElement)) {
      e.preventDefault();
      first.focus();
    } else if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      lastItem.focus();
    } else if (!e.shiftKey && document.activeElement === lastItem) {
      e.preventDefault();
      first.focus();
    }
  }
}
watch(
  () => !!activeTour.value,
  (on) => {
    if (on) window.addEventListener("keydown", onKey, true);
    else window.removeEventListener("keydown", onKey, true);
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  stop();
  cancelAnimationFrame(frame);
  window.removeEventListener("keydown", onKey, true);
});
</script>

<template>
  <Teleport to="body">
    <div v-if="activeTour && activeStep" class="fixed inset-0 z-[90]" data-testid="tour">
      <!-- The page is dimmed; the element of the step is a hole in the dimming. Both swallow clicks, so the page under the tour is not touched. -->
      <div v-if="!box" class="absolute inset-0 bg-black/55" aria-hidden="true"></div>
      <div
        v-else
        class="pointer-events-none absolute rounded-2xl shadow-[0_0_0_9999px_rgb(0_0_0/0.55)] ring-2 ring-white/70 transition-all duration-200 motion-reduce:transition-none"
        :style="{ left: `${box.x}px`, top: `${box.y}px`, width: `${box.w}px`, height: `${box.h}px` }"
        aria-hidden="true"
        data-testid="tour-spotlight"
      ></div>
      <div v-if="box" class="absolute inset-0" aria-hidden="true"></div>

      <div
        ref="card"
        role="dialog"
        aria-modal="true"
        aria-labelledby="tour-title"
        aria-describedby="tour-body"
        class="bg-card text-card-foreground border-border/60 shadow-pop absolute flex w-[21.5rem] max-w-[calc(100vw-1.5rem)] flex-col gap-3 rounded-3xl border p-5"
        :style="{ left: `${pos.x}px`, top: `${pos.y}px` }"
        data-testid="tour-card"
      >
        <p class="text-muted-foreground text-xs font-medium tabular-nums" role="status" data-testid="tour-progress">{{ t("tour.progress", { n: index + 1, total }) }}</p>
        <h2 id="tour-title" class="text-lg leading-snug font-semibold tracking-tight">{{ t(activeStep.title) }}</h2>
        <p id="tour-body" class="text-muted-foreground text-sm leading-relaxed">{{ t(activeStep.body) }}</p>
        <div class="flex items-center gap-1.5" aria-hidden="true">
          <span v-for="n in total" :key="n" :class="['h-1.5 rounded-full transition-all', n - 1 === index ? 'bg-brand w-5' : 'bg-border w-1.5']"></span>
        </div>
        <div class="flex items-center justify-between gap-2 pt-1">
          <Button type="button" variant="ghost" size="sm" data-testid="tour-skip" @click="endTour()">{{ t("tour.skip") }}</Button>
          <div class="flex items-center gap-2">
            <Button v-if="index > 0" type="button" variant="outline" size="sm" data-testid="tour-back" @click="prevStep()">{{ t("tour.back") }}</Button>
            <Button type="button" size="sm" data-tour-primary data-testid="tour-next" @click="nextStep()">{{ last ? t("tour.done") : t("tour.next") }}</Button>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>
