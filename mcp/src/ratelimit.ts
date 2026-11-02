import { createHash } from "node:crypto";

/**
 * A limit on how fast one API key may call tools through this server. The gateway limits per tenant, so every key of a tenant shares one budget and an assistant that loops
 * spends the application's; this is the key's own share, kept by the server that the assistant talks to (the gateway has no per-key limit: docs/plans/20-mcp.md). It does not
 * limit a caller that skips this server and calls the API directly with the same key.
 *
 * A token bucket per key: `perMinute` calls a minute on average, a burst of a sixth of that (at least one) at once. A call over the limit is refused with the seconds to wait.
 */
export interface RateLimit {
  /** Charges one call to `key`; 0 when it may go ahead, else the whole seconds to wait before the next may. */
  take(key: string): number;
}

/** How many keys are remembered; a server that is sent keys that do not exist forgets the oldest. */
const MAX_KEYS = 10_000;

/** `perMinute` of 0 (or not a positive number) is no limit: undefined. `now` is injected in tests. */
export function createRateLimit(perMinute: number, now: () => number = Date.now): RateLimit | undefined {
  if (!(perMinute > 0)) return undefined;
  const burst = Math.max(1, Math.ceil(perMinute / 6));
  const perMs = perMinute / 60_000;
  const buckets = new Map<string, { tokens: number; at: number }>();
  return {
    take(key) {
      const t = now();
      const b = buckets.get(key) ?? { tokens: burst, at: t };
      buckets.delete(key); // re-inserted below: a key in use is the youngest, so the oldest to forget is the one idle the longest
      b.tokens = Math.min(burst, b.tokens + (t - b.at) * perMs);
      b.at = t;
      let wait = 0;
      if (b.tokens >= 1) b.tokens -= 1;
      else wait = Math.max(1, Math.ceil((1 - b.tokens) / perMs / 1000));
      buckets.set(key, b);
      if (buckets.size > MAX_KEYS) buckets.delete(buckets.keys().next().value as string);
      return wait;
    },
  };
}

/**
 * What a key is called in the limiter: a hash of the whole key, not its public prefix. The prefix is shown in consoles and logs, so a limit kept by prefix could be spent
 * by anyone who knows it; the hash can be reached only by whoever holds the secret.
 */
export const limitKeyOf = (apiKey: string): string => createHash("sha256").update(apiKey).digest("hex");
