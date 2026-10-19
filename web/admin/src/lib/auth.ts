"use client";

import { useSyncExternalStore } from "react";
import { getToken } from "./api";

function subscribe(onChange: () => void): () => void {
  window.addEventListener("storage", onChange);
  window.addEventListener("quotify-auth", onChange);
  return () => {
    window.removeEventListener("storage", onChange);
    window.removeEventListener("quotify-auth", onChange);
  };
}

/** The operator token of this tab (null before hydration, and when signed out). */
export function useToken(): string | null {
  return useSyncExternalStore(subscribe, getToken, () => null);
}

const noop = () => () => {};

/** False while the server-rendered HTML is hydrated, true after: for code that must only run in the browser. */
export function useHydrated(): boolean {
  return useSyncExternalStore(noop, () => true, () => false);
}
