import { computed, readonly, ref } from "vue";

const OPERATOR_KEY = "quotify.operatorToken";
const PERSON_KEY = "quotify.sessionToken";
const ACCOUNT_KEY = "quotify.account";

function read(key: string): string | null {
  try {
    return window.sessionStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string | null): void {
  try {
    if (value === null) window.sessionStorage.removeItem(key);
    else window.sessionStorage.setItem(key, value);
  } catch {
    // Storage can be blocked; the token then lives for the life of the page only.
  }
}

const operator = ref<string | null>(read(OPERATOR_KEY));
const person = ref<string | null>(read(PERSON_KEY));
const account = ref<string | null>(read(ACCOUNT_KEY));

/**
 * The operator token of this tab: kept in session storage, so it dies with the tab and is never written anywhere else.
 * A person's session token (qs_…, from /auth/signin) is kept the same way: in this tab only, never in localStorage, and cleared at sign-out.
 * The two are never held together: signing in as one forgets the other.
 */
export const operatorToken = readonly(operator);
export const personToken = readonly(person);
export const isPerson = computed(() => person.value !== null);
/** The account a person with several memberships is working in ("" or null: their first). Kept in this tab only, like the token. */
export const activeAccount = readonly(account);
export const isOperator = computed(() => operator.value !== null);

/** The bearer credential for the gateway: whichever of the two this tab holds. */
export function currentToken(): string | null {
  return operator.value ?? person.value;
}

export function setOperatorToken(value: string | null): void {
  if (value !== null) setPersonToken(null);
  operator.value = value;
  write(OPERATOR_KEY, value);
}

export function setPersonToken(value: string | null): void {
  if (value !== null) {
    operator.value = null;
    write(OPERATOR_KEY, null);
  }
  person.value = value;
  write(PERSON_KEY, value);
  // A new sign-in starts in the person's first account; a sign-out forgets the choice.
  setActiveAccount(null);
}

export function setActiveAccount(id: string | null): void {
  account.value = id;
  write(ACCOUNT_KEY, id);
}

type Listener = () => void;
const lostListeners = new Set<Listener>();

/** Called when the gateway says the person's session is over (expired, revoked, signed out elsewhere). */
export function onSessionLost(listener: Listener): () => void {
  lostListeners.add(listener);
  return () => lostListeners.delete(listener);
}

/** Forgets a session the gateway no longer accepts and tells the app, once. */
export function sessionLost(): void {
  if (person.value === null) return;
  setPersonToken(null);
  for (const l of lostListeners) l();
}

/** Where the tenant pages live for whoever is signed in: an operator reaches any tenant at /tenants, a person only their apps' tenants at /app/t. */
export function tenantBase(): string {
  return person.value !== null ? "/app/t" : "/tenants";
}
