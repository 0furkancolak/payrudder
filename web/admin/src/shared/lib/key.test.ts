import { describe, expect, it } from "vitest";
import { newKey, useAttemptKey } from "./key";

describe("idempotency keys", () => {
  it("makes a new key every time asked", () => {
    expect(newKey()).toMatch(/^[0-9a-f-]{36}$/);
    expect(newKey()).not.toBe(newKey());
  });

  it("keeps one key for as long as the attempt is retried, and a new one once it is renewed", () => {
    const attempt = useAttemptKey();
    const first = attempt.key.value;
    // a retry after a timeout reads the same key
    expect(attempt.key.value).toBe(first);
    attempt.renew();
    expect(attempt.key.value).not.toBe(first);
  });
});
