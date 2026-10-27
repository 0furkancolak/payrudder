import { nextTick, ref } from "vue";
import { describe, expect, it } from "vitest";
import { useFormErrors } from "./formErrors";

describe("useFormErrors", () => {
  it("shows nothing before the first attempt, then every problem, then follows the fields", async () => {
    const name = ref("");
    const form = useFormErrors(() => ({ name: name.value.trim() === "" ? "required" : undefined }));
    expect(form.errors.value).toEqual({});
    expect(form.valid.value).toBe(false);
    expect(form.attempt()).toBe(false);
    await nextTick();
    expect(form.errors.value).toEqual({ name: "required" });
    name.value = "Ada";
    expect(form.errors.value).toEqual({});
    expect(form.attempt()).toBe(true);
  });
  it("forgets the attempt on reset", () => {
    const form = useFormErrors(() => ({ a: "x" }));
    form.attempt();
    expect(form.errors.value).toEqual({ a: "x" });
    form.reset();
    expect(form.errors.value).toEqual({});
  });
});
