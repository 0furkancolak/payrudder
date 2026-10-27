import { describe, expect, it } from "vitest";
import { keepEnv } from "./link";

describe("keepEnv", () => {
  it("carries the environment of the page the link is on", () => {
    expect(keepEnv({ env: "sandbox", sheet: "customer:x" }, "subscription", "s1")).toEqual({ env: "sandbox", subscription: "s1" });
    expect(keepEnv({ env: "live" }, "invoice", "i1")).toEqual({ env: "live", invoice: "i1" });
  });

  it("adds nothing when the page names no environment, or a strange one", () => {
    expect(keepEnv({}, "invoice", "i1")).toEqual({ invoice: "i1" });
    expect(keepEnv({ env: "moon" }, "invoice", "i1")).toEqual({ invoice: "i1" });
  });
});
