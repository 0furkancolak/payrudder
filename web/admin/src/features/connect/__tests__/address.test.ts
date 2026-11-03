import { describe, expect, it } from "vitest";
import { LOCAL_API_ADDRESS, initialAddress } from "../address";

describe("the address the code starts with", () => {
  it("is the one the person typed before", () => {
    expect(initialAddress("https://mine.example.com", "https://api.example.com")).toBe("https://mine.example.com");
  });
  it("is the deployment's own address when nothing was typed", () => {
    expect(initialAddress("", "https://api.example.com")).toBe("https://api.example.com");
    expect(initialAddress("  ", " https://api.example.com ")).toBe("https://api.example.com");
  });
  it("is a local stack when the deployment sets none", () => {
    expect(initialAddress("", undefined)).toBe(LOCAL_API_ADDRESS);
    expect(initialAddress("", "")).toBe("http://localhost:42800");
  });
});
