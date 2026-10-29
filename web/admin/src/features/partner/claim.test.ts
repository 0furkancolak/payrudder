import { QueryClient } from "@tanstack/vue-query";
import { describe, expect, it, vi } from "vitest";
import { invalidateAfterClaim, partnerKeys } from "./api";

// The rule the partner home relies on: a claim that linked records makes the list (and the profile's memberships) be read again; a claim that linked nothing does not.
describe("invalidateAfterClaim", () => {
  const spy = () => {
    const queries = new QueryClient();
    return { queries, invalidate: vi.spyOn(queries, "invalidateQueries").mockResolvedValue() };
  };

  it("reads the list and the profile again when something was claimed", () => {
    const { queries, invalidate } = spy();
    invalidateAfterClaim(queries, 1);
    expect(invalidate.mock.calls.map((c) => (c[0] as { queryKey?: unknown }).queryKey)).toEqual([partnerKeys.list, ["app", "me"]]);
  });

  it("touches nothing when nothing was claimed", () => {
    for (const claimed of [0, undefined, -1]) {
      const { queries, invalidate } = spy();
      invalidateAfterClaim(queries, claimed);
      expect(invalidate).not.toHaveBeenCalled();
    }
  });
});
