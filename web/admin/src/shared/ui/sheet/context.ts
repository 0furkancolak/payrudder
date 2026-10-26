import type { ComputedRef, InjectionKey } from "vue";

/** What a sheet tells its parts: its place in the stack (uid) and whether it is open. */
export interface SheetContext {
  uid: number;
  open: ComputedRef<boolean>;
}
export const SHEET_CONTEXT: InjectionKey<SheetContext> = Symbol("sheet");
