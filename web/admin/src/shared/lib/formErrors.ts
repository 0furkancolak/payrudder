import { computed, nextTick, ref } from "vue";

/**
 * The console's one pattern for checking a form before it is sent. The form lists its own rules (a message per field that is wrong, nothing for a field
 * that is fine); nothing is shown while the person is still typing their first answer. The first attempt to submit turns the messages on, puts them next
 * to their fields and moves the focus to the first wrong one, and from then on they follow what is typed (an error goes away when its field is fixed).
 * The submit button stays enabled: a disabled button that does not say why is what this replaces.
 */
export function useFormErrors<K extends string>(check: () => Partial<Record<K, string>>) {
  const attempted = ref(false);
  const all = computed(() => {
    const out: Partial<Record<K, string>> = {};
    for (const [k, v] of Object.entries(check()) as [K, string | undefined][]) if (v) out[k] = v;
    return out;
  });
  /** The messages to show now: none before the first attempt. */
  const errors = computed(() => (attempted.value ? all.value : ({} as Partial<Record<K, string>>)));
  const valid = computed(() => Object.keys(all.value).length === 0);
  /** Call from the submit handler: true when the form may be sent. Otherwise the messages are on and the first wrong field has the focus. */
  function attempt(): boolean {
    attempted.value = true;
    if (valid.value) return true;
    void nextTick(() => focusFirstInvalid());
    return false;
  }
  /** Forgets the attempt (a sheet that opens clean). */
  function reset() {
    attempted.value = false;
  }
  return { errors, valid, attempted, attempt, reset };
}

/** Moves the focus to the first control in the open sheet or dialog (or the page) that is marked invalid. */
export function focusFirstInvalid(root: ParentNode | undefined = typeof document === "undefined" ? undefined : document): boolean {
  if (!root) return false;
  const scope = root.querySelector('[role="dialog"][data-state="open"]') ?? root;
  const el = scope.querySelector<HTMLElement>('[aria-invalid="true"]');
  el?.focus();
  return el !== null && el !== undefined;
}
