import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { effectScope, nextTick, ref, type EffectScope } from "vue";
import { names, sheetName } from "./sheets";
import { useSheetTarget } from "./useSheetTarget";

type Expense = { id: string; vendor: string };
const expenses = ref<Expense[]>([]);
// The watchers of one test must not outlive it: they would answer the next test's address.
let scope: EffectScope;

function setup() {
  const open = ref(false);
  const target = ref<Expense | null>(null);
  scope.run(() => useSheetTarget("void-expense", open, target, { id: (e) => e.id, find: (id) => expenses.value.find((e) => e.id === id) }));
  return { open, target };
}

beforeEach(() => {
  scope = effectScope();
  names.value = [];
  expenses.value = [
    { id: "a", vendor: "Acme" },
    { id: "b:1,2", vendor: "Odd" },
  ];
});

afterEach(() => scope.stop());

describe("a sheet bound to a record", () => {
  it("writes the record into the address while it is open, and takes it off when closed", () => {
    const { open, target } = setup();
    target.value = expenses.value[0] ?? null;
    expect(names.value).toEqual([]);
    open.value = true;
    expect(names.value).toEqual(["void-expense:a"]);
    open.value = false;
    expect(names.value).toEqual([]);
  });

  it("escapes what separates names and parts", () => {
    const { open, target } = setup();
    target.value = expenses.value[1] ?? null;
    open.value = true;
    expect(names.value).toEqual([sheetName("void-expense", "b:1,2")]);
    expect(names.value[0]).not.toContain(",");
  });

  it("opens on the record the address names, and closes when the address stops naming it", async () => {
    names.value = ["void-expense:a"];
    const { open, target } = setup();
    expect(open.value).toBe(true);
    expect(target.value?.vendor).toBe("Acme");
    expect(names.value).toEqual(["void-expense:a"]);
    names.value = [];
    await nextTick();
    expect(open.value).toBe(false);
  });

  it("waits for the record to load and keeps the name meanwhile", async () => {
    expenses.value = [];
    names.value = ["void-expense:a"];
    const { open } = setup();
    expect(open.value).toBe(false);
    expect(names.value).toEqual(["void-expense:a"]);
    expenses.value = [{ id: "a", vendor: "Acme" }];
    await nextTick();
    expect(open.value).toBe(true);
  });

  it("follows the address to another record", async () => {
    names.value = ["void-expense:a"];
    const { target } = setup();
    names.value = [sheetName("void-expense", "b:1,2")];
    await nextTick();
    expect(target.value?.vendor).toBe("Odd");
  });

  it("restores through the caller's own function when the sheet has to be filled", () => {
    names.value = ["void-expense:a"];
    const open = ref(false);
    const target = ref<Expense | null>(null);
    const filled: string[] = [];
    scope.run(() =>
      useSheetTarget("void-expense", open, target, {
        id: (e) => e.id,
        find: (id) => expenses.value.find((e) => e.id === id),
        restore: (e) => {
          filled.push(e.vendor);
          target.value = e;
          open.value = true;
        },
      }),
    );
    expect(filled).toEqual(["Acme"]);
    expect(names.value).toEqual(["void-expense:a"]);
  });
});
