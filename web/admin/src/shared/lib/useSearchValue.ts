import { computed, type WritableComputedRef } from "vue";
import { searchParams, updateSearch } from "./sheets";

/** What the address holds under a key as text (the router reads `?customer=123` as a number), or undefined when it holds nothing. */
export function readSearchText(value: unknown): string | undefined {
  if (typeof value === "string") return value === "" ? undefined : value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return undefined;
}

/**
 * A choice of a page kept in the address as `?<key>=<value>`: the tab that is open, the customer picked. A reload, a shared link and "back" bring the same
 * choice. Writing the fallback (or an empty text) takes the key off the address, so the plain address stays the plain page.
 */
export function useSearchValue(key: string, fallback = ""): WritableComputedRef<string> {
  return computed({
    get: () => readSearchText(searchParams.value[key]) ?? fallback,
    set: (v) => updateSearch((prev) => ({ ...prev, [key]: v === "" || v === fallback ? undefined : v })),
  });
}

/** Like `useSearchValue`, for a choice among a fixed set of names: an address that holds anything else shows the fallback. */
export function useTabParam<T extends string>(key: string, allowed: readonly T[], fallback: T): WritableComputedRef<T> {
  const raw = useSearchValue(key, fallback);
  return computed({
    get: () => (allowed.includes(raw.value as T) ? (raw.value as T) : fallback),
    set: (v) => (raw.value = v),
  });
}
