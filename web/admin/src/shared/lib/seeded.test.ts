import { nextTick, ref } from "vue";
import { describe, expect, it } from "vitest";
import { useSeededRef } from "./seeded";

describe("useSeededRef", () => {
  it("takes the first value the source gives and then leaves the ref to the person", async () => {
    const source = ref("");
    const value = useSeededRef(source, "USD");
    expect(value.value).toBe("USD");
    source.value = "TRY";
    await nextTick();
    expect(value.value).toBe("TRY");
    value.value = "EUR"; // the person picks another one
    source.value = "GBP";
    await nextTick();
    expect(value.value).toBe("EUR");
  });

  it("is seeded at once when the source already has a value", () => {
    expect(useSeededRef(() => "JPY", "USD").value).toBe("JPY");
  });
});
