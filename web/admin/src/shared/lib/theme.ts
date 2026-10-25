import { ref } from "vue";
import { safeGet, safeSet } from "./storage";

export type ThemeChoice = "system" | "light" | "dark";

const KEY = "payrudder.theme";

function read(): ThemeChoice {
  const v = safeGet("local", KEY);
  return v === "light" || v === "dark" ? v : "system";
}

/** The person's choice. "system" follows prefers-color-scheme (the CSS does that by itself); the other two are written on <html data-theme>. */
export const theme = ref<ThemeChoice>(read());

export function applyTheme(choice: ThemeChoice = theme.value): void {
  theme.value = choice;
  const root = document.documentElement;
  if (choice === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", choice);
  safeSet("local", KEY, choice === "system" ? null : choice);
}

/** Whether a choice shows the dark colours, given whether the system prefers them (which only the "system" choice follows). */
export function isDarkFor(choice: ThemeChoice, systemDark: boolean): boolean {
  return choice === "system" ? systemDark : choice === "dark";
}
