import { ref, type Ref } from "vue";

/** What a combobox asks an API for its options: the text typed, and a signal that is aborted when the answer is no longer wanted. */
export type OptionsLoader<T> = (text: string, signal: AbortSignal) => Promise<T[]>;

/**
 * The asking side of a combobox that gets its options from an API: `ask(text)` waits `debounce` ms of quiet (`ask(text, true)` does not wait), then calls
 * `load(text, signal)`. Only the answer to the last question is kept: an older answer that arrives late is dropped and its request is aborted through the
 * signal. `status` is "loading" from the moment of asking until the answer (or the failure) arrives; a failure keeps the error in `failure`; `stop()`
 * forgets whatever is on its way (the list closed, the component went away).
 */
export function useAsyncOptions<T>(load: () => OptionsLoader<T> | undefined, debounce: () => number) {
  const answer = ref<T[]>([]) as Ref<T[]>;
  const status = ref<"idle" | "loading" | "error">("idle");
  const failure = ref<unknown>(null);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let asked = 0;
  let abort: AbortController | null = null;
  let last = "";

  async function run(text: string) {
    const fn = load();
    if (!fn) return;
    abort?.abort();
    const mine = ++asked;
    const controller = new AbortController();
    abort = controller;
    status.value = "loading";
    try {
      const got = await fn(text, controller.signal);
      if (mine !== asked) return; // a newer question was asked meanwhile: this answer is stale
      answer.value = got;
      failure.value = null;
      status.value = "idle";
    } catch (e) {
      if (mine !== asked || controller.signal.aborted) return;
      failure.value = e;
      status.value = "error";
    }
  }

  function ask(text: string, now = false) {
    if (!load()) return;
    last = text;
    clearTimeout(timer);
    status.value = "loading";
    failure.value = null;
    timer = setTimeout(() => void run(text), now ? 0 : debounce());
  }

  return {
    answer,
    status,
    failure,
    ask,
    /** Asks again what was asked last (the retry button). */
    again: () => ask(last, true),
    /** Forgets the answer (the list opens empty and loads afresh). */
    reset() {
      answer.value = [];
    },
    stop() {
      clearTimeout(timer);
      asked++; // whatever is on its way is stale now
      abort?.abort();
      if (status.value === "loading") status.value = "idle";
    },
  };
}
