import { describe, expect, it } from "vitest";
import { canDeleteApp, canRenameApp, confirmsDelete, nameToSave } from "./appManage";

describe("who manages an app", () => {
  it("lets owners and admins rename, and owners alone delete", () => {
    expect([canRenameApp("owner"), canRenameApp("admin"), canRenameApp("viewer"), canRenameApp(undefined)]).toEqual([true, true, false, false]);
    expect([canDeleteApp("owner"), canDeleteApp("admin"), canDeleteApp("viewer"), canDeleteApp(undefined)]).toEqual([true, false, false, false]);
  });
});

describe("renaming", () => {
  it("saves a trimmed new name and nothing else", () => {
    expect(nameToSave("  Shop 2 ", "Shop")).toBe("Shop 2");
    expect(nameToSave("Shop", "Shop")).toBe("");
    expect(nameToSave(" Shop ", "Shop")).toBe("");
    expect(nameToSave("   ", "Shop")).toBe("");
    expect(nameToSave("Shop", undefined)).toBe("Shop");
  });
});

describe("confirming a delete", () => {
  it("needs the whole name, spelled as it is", () => {
    expect(confirmsDelete("Web shop", "Web shop")).toBe(true);
    expect(confirmsDelete("  Web shop ", "Web shop")).toBe(true);
    expect(confirmsDelete("web shop", "Web shop")).toBe(false);
    expect(confirmsDelete("Web", "Web shop")).toBe(false);
    expect(confirmsDelete("Web  shop", "Web shop")).toBe(false);
    expect(confirmsDelete("", "Web shop")).toBe(false);
  });

  it("confirms nothing while the name is not known", () => {
    expect(confirmsDelete("", undefined)).toBe(false);
    expect(confirmsDelete("", "")).toBe(false);
    expect(confirmsDelete("x", undefined)).toBe(false);
  });
});
