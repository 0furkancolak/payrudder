<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { clearAt, digitsOf, pasteStart, spread } from "./otp";

/**
 * The six boxes of a one-time code. Typing a digit moves on to the next box, Backspace on an empty box goes back, the arrow keys move, a pasted code (or
 * a code the phone's keyboard offers from the mail, which arrives as one value in the first box) fills all six, and anything that is not a digit is dropped.
 * Digits always run left to right, whatever the language's direction. The parent gets `complete` when the sixth is in.
 */
const props = withDefaults(defineProps<{ length?: number; disabled?: boolean; invalid?: boolean }>(), { length: 6 });
const model = defineModel<string>({ required: true });
const emit = defineEmits<{ complete: [code: string] }>();
const { t } = useI18n();

const cells = ref<string[]>(Array.from({ length: props.length }, (_, i) => model.value[i] ?? ""));
const boxes = ref<HTMLInputElement[]>([]);
const code = computed(() => cells.value.join(""));

// The parent clears the code after a refusal: the boxes follow.
watch(model, (v) => {
  if (v !== code.value) cells.value = Array.from({ length: props.length }, (_, i) => v[i] ?? "");
});

function commit(): void {
  model.value = code.value;
  if (code.value.length === props.length) emit("complete", code.value);
}

async function focus(i: number): Promise<void> {
  await nextTick();
  const box = boxes.value[Math.max(0, Math.min(props.length - 1, i))];
  box?.focus();
  box?.select();
}

/** Puts digits into the boxes from `start` on and focuses the box after the last one filled. */
function fill(start: number, raw: string): void {
  const r = spread(cells.value, start, raw);
  cells.value = r.cells;
  commit();
  void focus(r.next);
}

function onInput(i: number, event: Event): void {
  const el = event.target as HTMLInputElement;
  const digits = digitsOf(el.value);
  if (digits === "") {
    cells.value = clearAt(cells.value, i);
    el.value = "";
    commit();
    return;
  }
  // One digit goes in this box; more (autofill, a paste the browser did not hand to onPaste) spread over this box and the ones after it.
  if (digits.length === 1) el.value = digits;
  fill(i, digits);
}

function onPaste(i: number, event: ClipboardEvent): void {
  event.preventDefault();
  const digits = digitsOf(event.clipboardData?.getData("text") ?? "");
  if (digits !== "") fill(pasteStart(digits.length, props.length, i), digits);
}

function onKeydown(i: number, event: KeyboardEvent): void {
  if (event.key === "Backspace" && cells.value[i] === "" && i > 0) {
    event.preventDefault();
    cells.value = clearAt(cells.value, i - 1);
    commit();
    void focus(i - 1);
  } else if (event.key === "ArrowLeft") {
    event.preventDefault();
    void focus(i - 1);
  } else if (event.key === "ArrowRight") {
    event.preventDefault();
    void focus(i + 1);
  }
}

defineExpose({ focusFirst: () => focus(0) });
</script>

<template>
  <div dir="ltr" class="flex justify-center gap-2 sm:gap-3" role="group" :aria-label="t('product.signIn.code.label')">
    <input
      v-for="n in length"
      :key="n"
      :ref="(el) => { if (el) boxes[n - 1] = el as HTMLInputElement; }"
      :value="cells[n - 1]"
      type="text"
      inputmode="numeric"
      pattern="[0-9]*"
      :maxlength="length"
      :autocomplete="n === 1 ? 'one-time-code' : 'off'"
      :disabled="disabled"
      :aria-label="t('product.signIn.code.digit', { n, total: length })"
      :aria-invalid="invalid ? 'true' : undefined"
      class="border-input bg-card focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 tabular h-12 w-10 rounded-xl border text-center text-xl font-semibold outline-none focus-visible:ring-3 disabled:opacity-50 sm:h-14 sm:w-12 sm:text-2xl"
      @input="onInput(n - 1, $event)"
      @paste="onPaste(n - 1, $event)"
      @keydown="onKeydown(n - 1, $event)"
      @focus="($event.target as HTMLInputElement).select()"
    />
  </div>
</template>
