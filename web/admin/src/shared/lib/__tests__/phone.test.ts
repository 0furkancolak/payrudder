import { describe, expect, it } from "vitest";
import { normalizePhone } from "../phone";

// The same cases as pkg/phone/phone_test.go: the form and the server must not disagree.
describe("normalizePhone", () => {
  it("writes an international number as E.164", () => {
    for (const [raw, want] of Object.entries({
      "": "",
      "+905321234567": "+905321234567",
      "+90 532 123 45 67": "+905321234567",
      "+90 (532) 123-45-67": "+905321234567",
      "00905321234567": "+905321234567",
      "00 49 30 123456": "+4930123456",
      "+1.415.555.2671": "+14155552671",
      "+1234567": "+1234567",
      "+123456789012345": "+123456789012345",
    })) {
      expect(normalizePhone(raw), raw).toBe(want);
    }
  });
  it("refuses a national or malformed number", () => {
    for (const raw of ["05321234567", "5321234567", "+0905321234567", "+12345", "+1234567890123456", "+90 532 abc 45 67", "+", "++905321234567", "+90 532 123 45 67 ext 2"]) {
      expect(normalizePhone(raw), raw).toBeNull();
    }
  });
});
