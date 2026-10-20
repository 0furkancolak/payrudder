import { describe, expect, it } from "vitest";
import { docActions, docVariant, errorKeyPath } from "./status";

describe("e-invoice status helpers", () => {
  it("colours by status", () => {
    expect(docVariant("issued")).toBe("success");
    expect(docVariant("failed")).toBe("destructive");
    expect(docVariant("pending")).toBe("secondary");
    expect(docVariant("cancelled")).toBe("secondary");
    expect(docVariant(undefined)).toBe("secondary");
  });

  it("offers retry only on a failed document and cancel only on an issued one", () => {
    expect(docActions({ status: "failed" })).toEqual({ retry: true, cancel: false });
    expect(docActions({ status: "issued" })).toEqual({ retry: false, cancel: true });
    expect(docActions({ status: "pending" })).toEqual({ retry: false, cancel: false });
    expect(docActions({ status: "cancelled" })).toEqual({ retry: false, cancel: false });
  });

  it("maps an error key to its message path", () => {
    expect(errorKeyPath("einvoice.provider.rejected")).toBe("einvoice.errorKeys.einvoice.provider.rejected");
    expect(errorKeyPath("")).toBe("");
  });
});
