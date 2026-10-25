/**
 * Runs asynchronous work where only the latest call may apply its result: when a slower earlier call finishes after a newer one has started, its result is
 * dropped. For choices that load something before they take effect (a language file): picking TR and then, at once, DE must end in DE however long TR takes.
 *
 * `work` runs for every call; `apply` runs only if no later call began while it was running.
 */
export function latestOnly(): <T>(work: () => Promise<T>, apply: (value: T) => void) => Promise<void> {
  let latest = 0;
  return async (work, apply) => {
    const mine = ++latest;
    const value = await work();
    if (mine === latest) apply(value);
  };
}
