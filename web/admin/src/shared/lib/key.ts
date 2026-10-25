import { ref } from "vue";

/** A fresh idempotency key. */
export function newKey(): string {
  return crypto.randomUUID();
}

/**
 * The idempotency key of one attempt at a form: it stays the same while the person retries the same submission (a request that timed out may or may not have
 * reached the server, and the server then answers the retry with the first result instead of doing it twice), and is renewed only when the attempt is
 * over, because it succeeded or because what is being submitted changed. A key made inside the request function would be new on every try and protect nothing.
 */
export function useAttemptKey() {
  const key = ref(newKey());
  return { key, renew: () => void (key.value = newKey()) };
}
