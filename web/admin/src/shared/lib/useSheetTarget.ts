import { getCurrentInstance, onBeforeUnmount, watch, type Ref } from "vue";
import { idOf, setKind, sheetKey } from "./sheets";

/**
 * A sheet that works on one record the page holds in `target` (the expense being voided, the customer whose address is edited) and is open while `open`
 * is true. While it is open its name, `<kind>:<id>`, is in the address, so a reload or a shared link brings it back: once `find` knows the record (the
 * list it comes from has loaded) the target is set to it and the sheet opens. A name the address no longer holds closes the sheet; one whose record is
 * not found waits, and stays in the address, until it is. A sheet is open for one record at a time. A sheet that has to be filled from the record when it opens passes `restore`, which sets the target and opens it.
 */
export function useSheetTarget<T>(kind: string, open: Ref<boolean>, target: Ref<T | null>, opts: { id: (t: T) => string; find: (id: string) => T | null | undefined; restore?: (found: T) => void }) {
  // The address named the record: the name is already in it while the target and the open flag are set one after the other.
  let restoring = false;
  // Synchronous: the watcher below closes the sheet when the address names no record, and must not run between the sheet opening and the address hearing of it.
  watch(
    [open, target],
    ([isOpen, t]) => {
      if (!restoring) setKind(kind, isOpen && t !== null ? sheetKey(opts.id(t)) : null);
    },
    { flush: "sync" },
  );
  watch(
    () => {
      const named = idOf(kind);
      return [named, named === undefined ? undefined : opts.find(decodeURIComponent(named))] as const;
    },
    ([named, found]) => {
      if (named === undefined) {
        if (open.value) open.value = false;
        return;
      }
      if (open.value && target.value !== null && sheetKey(opts.id(target.value)) === named) return;
      if (found === undefined || found === null) return;
      restoring = true;
      if (opts.restore) opts.restore(found);
      else {
        target.value = found;
        open.value = true;
      }
      restoring = false;
    },
    { immediate: true },
  );
  if (getCurrentInstance()) {
    onBeforeUnmount(() => {
      if (open.value) setKind(kind, null);
    });
  }
}
