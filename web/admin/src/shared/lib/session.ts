import { computed, readonly, ref } from "vue";

const PERSON_KEY = "quotify.sessionToken";
const OPERATOR_KEY = "quotify.operator";
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

const person = ref<string | null>(read(PERSON_KEY));
const operator = ref<boolean>(read(OPERATOR_KEY) === "1");
const account = ref<string | null>(read(ACCOUNT_KEY));

/**
 * The session token of this tab (qs_…, from signing in with a password, a one-time code or Google), kept in session storage: this tab only, never in
 * localStorage, cleared at sign-out. There is one kind of session: a platform operator is a person whose session the server also accepts on the operator
 * routes. The operator token of the API is a machine credential and is never entered here.
 */
export const personToken = readonly(person);
export const isPerson = computed(() => person.value !== null);
/** The account a person with several memberships is working in ("" or null: their first). Kept in this tab only, like the token. */
export const activeAccount = readonly(account);
/**
 * Whether the server said this person is a platform operator (the `operator` field of the sign-in answer and of /auth/me). It only decides what the page
 * shows; every operator route is decided again by the server on each request from the session, so a changed value here opens nothing.
 */
export const isOperator = computed(() => person.value !== null && operator.value);

/** The bearer credential for the gateway: the session token of this tab. */
export function currentToken(): string | null {
  return person.value;
}

export function setPersonToken(value: string | null, isOperatorSession = false): void {
  person.value = value;
  write(PERSON_KEY, value);
  setOperatorFlag(value !== null && isOperatorSession);
  // A new sign-in starts in the person's first account; a sign-out forgets the choice.
  setActiveAccount(null);
}

/** Records the server's verdict on whether this session belongs to an operator (from /auth/me). */
export function setOperatorFlag(value: boolean): void {
  operator.value = value;
  write(OPERATOR_KEY, value ? "1" : null);
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

/** Where the tenant pages live in the current view: the operator's console shows any tenant at /tenants, a person's own apps' tenants are at /app/t. */
export function tenantBase(): string {
  return window.location.pathname.startsWith("/app/") ? "/app/t" : "/tenants";
}
