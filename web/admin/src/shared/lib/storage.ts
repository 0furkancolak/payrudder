/**
 * Reads and writes the browser's storage without ever throwing. Storage can be blocked (a private window, a policy, a sandboxed frame): a read then finds
 * nothing and a write is dropped, so a choice lasts for the life of the page and nothing else breaks.
 */
export type StorageArea = "local" | "session";

function area(which: StorageArea): Storage {
  return which === "local" ? window.localStorage : window.sessionStorage;
}

/** The saved text, or null when there is none or storage cannot be read. */
export function safeGet(which: StorageArea, key: string): string | null {
  try {
    return area(which).getItem(key);
  } catch {
    return null;
  }
}

/** Saves the text; null forgets the key. Returns whether storage took it. */
export function safeSet(which: StorageArea, key: string, value: string | null): boolean {
  try {
    if (value === null) area(which).removeItem(key);
    else area(which).setItem(key, value);
    return true;
  } catch {
    return false;
  }
}

/** The keys that start with `prefix`; none when storage cannot be read. */
export function safeKeys(which: StorageArea, prefix: string): string[] {
  try {
    const s = area(which);
    const out: string[] = [];
    for (let i = 0; i < s.length; i++) {
      const k = s.key(i);
      if (k?.startsWith(prefix)) out.push(k);
    }
    return out;
  } catch {
    return [];
  }
}
