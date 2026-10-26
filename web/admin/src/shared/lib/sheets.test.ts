import { beforeEach, describe, expect, it } from "vitest";
import { ahead, back, closeName, enter, forward, idOf, leave, names, openName, openTabs, raise, setKind } from "./sheets";

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
