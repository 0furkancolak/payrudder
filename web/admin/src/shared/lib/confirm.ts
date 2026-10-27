import { shallowRef } from "vue";

/** What a confirmation asks: the question (`title`), what follows from saying yes (`body`) and the label of the button that does it. */
export interface ConfirmOptions {
  title?: string;
  body: string;
  confirmLabel?: string;
  /** The default is a destructive (red) button; pass false for a confirmation that is not one. */
  destructive?: boolean;
}

interface Pending extends ConfirmOptions {
  resolve: (ok: boolean) => void;
}

/** The question on screen, or null. One at a time: asking again while one is open answers the first with "no". */
export const pendingConfirm = shallowRef<Pending | null>(null);

/**
 * The console's one way to ask before something that cannot be undone: it resolves to true when the person confirms in the styled dialog (mounted once,
 * in the app shell) and to false on Cancel, Escape or a click outside. It replaces the browser's `window.confirm`, which looks different on every
 * browser, cannot be translated or tested the same way, and was used for some destructive actions while others had a real dialog.
 */
export function askConfirm(options: ConfirmOptions): Promise<boolean> {
  pendingConfirm.value?.resolve(false);
  return new Promise<boolean>((resolve) => {
    pendingConfirm.value = { ...options, resolve };
  });
}

/** Answers the question on screen (used by the dialog). */
export function answerConfirm(ok: boolean): void {
  const p = pendingConfirm.value;
  pendingConfirm.value = null;
  p?.resolve(ok);
}
