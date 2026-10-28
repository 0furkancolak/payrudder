import { shallowReactive } from "vue";
import type { Component } from "vue";

export interface HeaderActionEntry {
  props: { label: string; icon?: Component; disabled?: boolean; title?: string; testId?: string };
  click: () => void;
}

/** The main buttons of the page on screen, in the order they mounted; the frame's header draws them. */
export const headerActions = shallowReactive<HeaderActionEntry[]>([]);

export function registerHeaderAction(entry: HeaderActionEntry): () => void {
  headerActions.push(entry);
  return () => {
    const i = headerActions.indexOf(entry);
    if (i >= 0) headerActions.splice(i, 1);
  };
}
