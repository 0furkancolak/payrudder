// Development convenience: the dev server and the local compose stack (PREFILL_PLAYGROUND, served as /env.js) pre-fill the operator playground's account
// form. A production build served without that variable prefills nothing. (The operator token is never served to a page; there is no token form.)
declare global {
  interface Window {
    __PAYRUDDER__?: { prefill?: boolean; brand?: string; demo?: boolean };
  }
}

export function prefillPlayground(): boolean {
  return window.__PAYRUDDER__?.prefill === true || import.meta.env.DEV;
}

export const playgroundDefaults = { name: "demo-account", currency: "TRY", timezone: "Europe/Istanbul", graceHours: "24" } as const;
