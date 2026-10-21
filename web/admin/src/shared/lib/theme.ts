import { ref } from "vue";

export type ThemeChoice = "system" | "light" | "dark";

const KEY = "quotify.theme";

function read(): ThemeChoice {
  try {
    const v = window.localStorage.getItem(KEY);
    if (v === "light" || v === "dark") return v;
  } catch {
    // storage blocked: follow the system
  }
  return "system";
}

/** The person's choice. "system" follows prefers-color-scheme (the CSS does that by itself); the other two are written on <html data-theme>. */
export const theme = ref<ThemeChoice>(read());

export function applyTheme(choice: ThemeChoice = theme.value): void {
  theme.value = choice;
  const root = document.documentElement;
  if (choice === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", choice);
  try {
    if (choice === "system") window.localStorage.removeItem(KEY);
    else window.localStorage.setItem(KEY, choice);
  } catch {
    // the choice lasts for this page only
  }
}

/** Whether the page is dark right now, whatever the reason. */
export function isDark(): boolean {
  const set = document.documentElement.getAttribute("data-theme");
  if (set) return set === "dark";
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}
