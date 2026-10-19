import { ref, readonly } from "vue";

const KEY = "quotify.operatorToken";

function read(): string | null {
  try {
    return window.sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
}

const token = ref<string | null>(read());

/** The operator token of this tab: kept in session storage, so it dies with the tab and is never written anywhere else. */
export const operatorToken = readonly(token);

export function currentToken(): string | null {
  return token.value;
}

export function setOperatorToken(value: string | null): void {
  token.value = value;
  try {
    if (value === null) window.sessionStorage.removeItem(KEY);
    else window.sessionStorage.setItem(KEY, value);
  } catch {
    // Storage can be blocked; the token then lives for the life of the page only.
  }
}
