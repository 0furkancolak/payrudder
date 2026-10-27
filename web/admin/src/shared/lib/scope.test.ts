import { describe, expect, it } from "vitest";
import { ALL_APPS, appInSearch, resolveScope } from "./scope";

const apps = [{ id: "A" }, { id: "B" }];

describe("appInSearch", () => {
  it("reads a non-empty text and nothing else", () => {
    expect(appInSearch({ app: "A" })).toBe("A");
    expect(appInSearch({ app: "all" })).toBe("all");
    expect(appInSearch({ app: "" })).toBeUndefined();
    expect(appInSearch({ app: 3 })).toBeUndefined();
    expect(appInSearch({})).toBeUndefined();
    expect(appInSearch(undefined)).toBeUndefined();
  });
});

describe("resolveScope", () => {
  it("is the app of the path before anything else", () => {
    expect(resolveScope({ pathApp: "B", addressApp: "A", apps, last: "A" })).toBe("B");
  });
  it("is the address' app, or all apps when that is what the address says", () => {
    expect(resolveScope({ addressApp: "B", apps, last: "A" })).toBe("B");
    expect(resolveScope({ addressApp: ALL_APPS, apps, last: "A" })).toBe(ALL_APPS);
  });
  it("is the app used last, else the first app: an app is always selected", () => {
    expect(resolveScope({ apps, last: "B" })).toBe("B");
    expect(resolveScope({ apps, last: "gone" })).toBe("A");
    expect(resolveScope({ apps })).toBe("A");
  });
  it("ignores an app that does not exist", () => {
    expect(resolveScope({ pathApp: "X", addressApp: "Y", apps })).toBe("A");
  });
  it("is nothing while there are no apps", () => {
    expect(resolveScope({ addressApp: ALL_APPS, apps: [] })).toBeNull();
    expect(resolveScope({ apps: undefined })).toBeNull();
  });
});
