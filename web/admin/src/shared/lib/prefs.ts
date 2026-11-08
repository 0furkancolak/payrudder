import { ref } from "vue";
import { safeGet, safeSet } from "./storage";

/**
 * Per-browser conveniences kept in localStorage: whether the sidebar is folded to its icon rail, which of its groups are folded, and whether privacy
 * mode hides money. Every read and
 * write is guarded, because storage can be blocked (a private window, a policy): the choice then lasts for the life of the page and nothing breaks.
 * Changes made in another tab arrive through the `storage` event, so two tabs of the console agree.
 */
const SIDEBAR_KEY = "payrudder.sidebar.collapsed";
const PRIVACY_KEY = "payrudder.privacy";
const FOLDED_KEY = "payrudder.sidebar.folded";

const readFlag = (key: string): boolean => safeGet("local", key) === "1";
const writeFlag = (key: string, on: boolean): void => void safeSet("local", key, on ? "1" : null);

/** The sidebar is open (icons and labels) unless the person folded it. */
export const sidebarCollapsed = ref(readFlag(SIDEBAR_KEY));

/** The sidebar groups the person folded (their keys, nav.ts); every group is open until folded. */
const readFolded = (v: string | null): string[] => (v ?? "").split(",").filter(Boolean);
export const foldedSections = ref<string[]>(readFolded(safeGet("local", FOLDED_KEY)));

/** Privacy mode: monetary amounts are replaced by a mask everywhere they are formatted (see money.ts), until the person turns it off. */
export const privacyMode = ref(readFlag(PRIVACY_KEY));

export function setSidebarCollapsed(on: boolean): void {
  sidebarCollapsed.value = on;
  writeFlag(SIDEBAR_KEY, on);
}

export function toggleSection(key: string): void {
  const now = foldedSections.value;
  foldedSections.value = now.includes(key) ? now.filter((k) => k !== key) : [...now, key];
  void safeSet("local", FOLDED_KEY, foldedSections.value.length ? foldedSections.value.join(",") : null);
}

export function setPrivacyMode(on: boolean): void {
  privacyMode.value = on;
  writeFlag(PRIVACY_KEY, on);
  document.documentElement.toggleAttribute("data-privacy", on);
}

/** Puts the saved state on <html> (for CSS and print) and starts following other tabs. Called once at start-up. */
export function startPrefs(): void {
  document.documentElement.toggleAttribute("data-privacy", privacyMode.value);
  window.addEventListener("storage", (e) => {
    if (e.key === SIDEBAR_KEY) sidebarCollapsed.value = e.newValue === "1";
    if (e.key === FOLDED_KEY) foldedSections.value = readFolded(e.newValue);
    if (e.key === PRIVACY_KEY) {
      privacyMode.value = e.newValue === "1";
      document.documentElement.toggleAttribute("data-privacy", privacyMode.value);
    }
  });
}
