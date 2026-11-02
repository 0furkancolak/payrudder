import { beforeEach, describe, expect, it, vi } from "vitest";
import { ahead, back, closeName, enter, forward, idOf, leave, names, openName, openTabs, raise, rememberOpener, restoreOpener, setKind, syncScrollLock } from "./sheets";

beforeEach(() => {
  names.value = [];
  ahead.value = [];
  for (const t of [...openTabs.value]) leave(t.uid);
});

describe("the stack of sheets", () => {
  it("keeps the names in the order they were opened, once each", () => {
    openName("new-customer");
    openName("invoice:1");
    openName("new-customer");
    expect(names.value).toEqual(["new-customer", "invoice:1"]);
    closeName("new-customer");
    expect(names.value).toEqual(["invoice:1"]);
  });

  it("puts the sheet of a kind on another record in the place of the one it showed", () => {
    setKind("invoice", "a");
    openName("new-note");
    setKind("invoice", "b");
    expect(names.value).toEqual(["invoice:b", "new-note"]);
    expect(idOf("invoice")).toBe("b");
    setKind("invoice", null);
    expect(idOf("invoice")).toBeUndefined();
    expect(names.value).toEqual(["new-note"]);
  });

  it("raises a tab to the front", () => {
    enter({ uid: 1, title: "A", close: () => undefined });
    enter({ uid: 2, title: "B", close: () => undefined });
    raise(1);
    expect(openTabs.value.map((t) => t.uid)).toEqual([2, 1]);
  });

  it("goes back by closing the front sheet and forward by opening its name again", async () => {
    openName("invoice:1");
    openName("new-note");
    enter({ uid: 1, title: "Invoice", close: () => closeName("invoice:1") });
    enter({ uid: 2, title: "Note", close: () => closeName("new-note") });
    await back();
    expect(names.value).toEqual(["invoice:1"]);
    expect(ahead.value).toEqual(["new-note"]);
    forward();
    expect(names.value).toEqual(["invoice:1", "new-note"]);
    expect(ahead.value).toEqual([]);
  });

  it("forgets what lies ahead when something else is opened", () => {
    ahead.value = ["x"];
    openName("new-thing");
    expect(ahead.value).toEqual([]);
  });
});

describe("the scroll lock of the stack", () => {
  /** A page that is scrolled to `y`, with a classic scrollbar of `bar` px. */
  const page = (y: number, bar = 0) => {
    const env = { root: { style: { overflow: "", paddingInlineEnd: "" } }, y: () => scrollAt.y, to: (to: number) => void (scrollAt.y = to), bar: () => bar };
    const scrollAt = { y };
    return { env, scrollAt };
  };

  it("locks the page at the first sheet and gives it back, at the same place, at the last", () => {
    const { env, scrollAt } = page(480, 15);
    syncScrollLock(true, env);
    expect(env.root.style.overflow).toBe("hidden");
    expect(env.root.style.paddingInlineEnd).toBe("15px");
    // A second sheet changes nothing, and nothing the page does behind them (a navigation that scrolls to the top) is kept.
    syncScrollLock(true, env);
    scrollAt.y = 0;
    syncScrollLock(false, env);
    expect(env.root.style.overflow).toBe("");
    expect(env.root.style.paddingInlineEnd).toBe("");
    expect(scrollAt.y).toBe(480);
  });

  it("keeps what the page's style said before, and does not pad for an overlay scrollbar", () => {
    const { env } = page(0, 0);
    env.root.style.overflow = "clip";
    syncScrollLock(true, env);
    expect(env.root.style.paddingInlineEnd).toBe("");
    syncScrollLock(false, env);
    expect(env.root.style.overflow).toBe("clip");
  });

  it("needs no document to open and close sheets, and unlocking a page that was never locked changes nothing", () => {
    enter({ uid: 1, title: "A", close: () => undefined });
    enter({ uid: 2, title: "B", close: () => undefined });
    leave(1);
    leave(2);
    expect(openTabs.value).toHaveLength(0);
    const { env, scrollAt } = page(90);
    syncScrollLock(false, env);
    expect(scrollAt.y).toBe(90);
  });
});

describe("the control a sheet gives the focus back to", () => {
  const button = (connected = true) => ({ isConnected: connected, focus: vi.fn() }) as unknown as HTMLElement;
  const closing = () => ({ preventDefault: vi.fn() }) as unknown as Event;

  it("is the one a row's menu offered when the sheet opened, and gets the focus when the last sheet closes", () => {
    const trigger = button();
    rememberOpener(trigger);
    enter({ uid: 1, title: "A", close: () => undefined });
    leave(1);
    const e = closing();
    restoreOpener(e);
    expect(trigger.focus).toHaveBeenCalledTimes(1);
    expect(e.preventDefault).toHaveBeenCalledTimes(1);
    // Once: a later sheet that nobody offered a control for does not go back to it.
    enter({ uid: 2, title: "B", close: () => undefined });
    leave(2);
    restoreOpener(closing());
    expect(trigger.focus).toHaveBeenCalledTimes(1);
  });

  it("waits while a sheet is still open behind, and is not used when the control has left the page", () => {
    const trigger = button();
    rememberOpener(trigger);
    enter({ uid: 1, title: "A", close: () => undefined });
    enter({ uid: 2, title: "B", close: () => undefined });
    leave(2);
    restoreOpener(closing());
    expect(trigger.focus).not.toHaveBeenCalled();
    leave(1);
    const gone = closing();
    const lost = button(false);
    rememberOpener(lost);
    enter({ uid: 3, title: "C", close: () => undefined });
    leave(3);
    restoreOpener(gone);
    expect(lost.focus).not.toHaveBeenCalled();
    expect(gone.preventDefault).not.toHaveBeenCalled();
  });

  it("is dropped when the action did not open a sheet soon after: a sheet opened much later is not about it", () => {
    vi.useFakeTimers();
    try {
      const trigger = button();
      rememberOpener(trigger);
      vi.advanceTimersByTime(5000);
      enter({ uid: 1, title: "A", close: () => undefined });
      leave(1);
      restoreOpener(closing());
      expect(trigger.focus).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});
