// Development convenience: the dev server and the local compose stack (PREFILL_OPERATOR_TOKEN, served as /env.js) pre-fill the
// operator token and the playground form. A production build served without that variable prefills nothing.
declare global {
  interface Window {
    __QUOTIFY__?: { operatorToken?: string; brand?: string; demo?: boolean };
  }
}

const LOCAL_TOKEN = "local-operator-token-change-me-0123456789";

export function prefilledOperatorToken(): string {
  const served = window.__QUOTIFY__?.operatorToken;
  if (served) return served;
  return import.meta.env.DEV ? LOCAL_TOKEN : "";
}

export const playgroundDefaults = { name: "demo-account", currency: "TRY", timezone: "Europe/Istanbul", graceHours: "24" } as const;
