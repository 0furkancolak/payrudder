import { createHmac } from "node:crypto";
import { describe, expect, it } from "vitest";
import { WebhookVerificationError, constructEvent, verifyWebhook } from "../src/index";

const secret = "whsec_test_secret";
const body = JSON.stringify({
  id: "0192",
  type: "invoice.paid",
  created: "2026-10-20T10:00:00Z",
  tenant_id: "t1",
  data: { invoice_id: "i1", total_minor: "1116", tax_minor: "186", net_minor: "930", status: "paid" },
});
const NOW = 1_700_000_000;

// The scheme exactly as docs/16-webhooks.md gives it, computed with node:crypto (independent of the WebCrypto code under test).
const sign = (t: number, payload: string | Buffer, key = secret) => createHmac("sha256", key).update(`${t}.`).update(payload).digest("hex");

async function reason(p: Promise<unknown>): Promise<string> {
  try {
    await p;
  } catch (e) {
    expect(e).toBeInstanceOf(WebhookVerificationError);
    return (e as WebhookVerificationError).reason;
  }
  return "no error";
}

describe("verifyWebhook", () => {
  it("accepts a valid signature (string, Uint8Array and ArrayBuffer payloads)", async () => {
    const header = `t=${NOW},v1=${sign(NOW, body)}`;
    await verifyWebhook(body, header, secret, 300, NOW);
    await verifyWebhook(new TextEncoder().encode(body), header, secret, 300, NOW);
    await verifyWebhook(new TextEncoder().encode(body).buffer, header, secret, 300, NOW);
  });

  it("verifies raw bytes that are not valid UTF-8 text", async () => {
    const bytes = Buffer.from([0xff, 0xfe, 0x00, 0x41]);
    await verifyWebhook(new Uint8Array(bytes), `t=${NOW},v1=${sign(NOW, bytes)}`, secret, 300, NOW);
  });

  it("rejects a tampered payload", async () => {
    const header = `t=${NOW},v1=${sign(NOW, body)}`;
    expect(await reason(verifyWebhook(body.replace("1116", "1"), header, secret, 300, NOW))).toBe("no_matching_signature");
  });

  it("rejects the wrong secret", async () => {
    const header = `t=${NOW},v1=${sign(NOW, body)}`;
    expect(await reason(verifyWebhook(body, header, "whsec_other", 300, NOW))).toBe("no_matching_signature");
  });

  it("rejects a signature over a different timestamp", async () => {
    const header = `t=${NOW},v1=${sign(NOW - 1, body)}`;
    expect(await reason(verifyWebhook(body, header, secret, 300, NOW))).toBe("no_matching_signature");
  });

  it("rejects stale and future timestamps, and honours the tolerance", async () => {
    const header = `t=${NOW},v1=${sign(NOW, body)}`;
    expect(await reason(verifyWebhook(body, header, secret, 300, NOW + 301))).toBe("timestamp_outside_tolerance");
    expect(await reason(verifyWebhook(body, header, secret, 300, NOW - 301))).toBe("timestamp_outside_tolerance");
    await verifyWebhook(body, header, secret, 300, NOW + 300);
    await verifyWebhook(body, header, secret, 1000, NOW + 900);
    expect(await reason(verifyWebhook(body, header, secret, 0, NOW + 1))).toBe("timestamp_outside_tolerance");
  });

  it("uses the real clock by default", async () => {
    const t = Math.floor(Date.now() / 1000);
    await verifyWebhook(body, `t=${t},v1=${sign(t, body)}`, secret);
    expect(await reason(verifyWebhook(body, `t=${t - 3600},v1=${sign(t - 3600, body)}`, secret))).toBe("timestamp_outside_tolerance");
  });

  it("accepts either of two signatures while a rotated secret is still valid", async () => {
    const oldSecret = "whsec_old";
    const header = `t=${NOW},v1=${sign(NOW, body, oldSecret)},v1=${sign(NOW, body)}`;
    await verifyWebhook(body, header, secret, 300, NOW);
    await verifyWebhook(body, header, oldSecret, 300, NOW);
    expect(await reason(verifyWebhook(body, header, "whsec_neither", 300, NOW))).toBe("no_matching_signature");
  });

  it.each([
    ["empty", ""],
    ["no signature", `t=${NOW}`],
    ["no timestamp", `v1=${"ab".repeat(32)}`],
    ["garbage", "hello"],
    ["non numeric t", `t=abc,v1=${"ab".repeat(32)}`],
  ])("rejects a malformed header: %s", async (_name, header) => {
    expect(await reason(verifyWebhook(body, header, secret, 300, NOW))).toBe("invalid_header");
  });

  it("ignores malformed or truncated hex signatures instead of crashing", async () => {
    const good = sign(NOW, body);
    expect(await reason(verifyWebhook(body, `t=${NOW},v1=zz,v1=abc,v1=${good.slice(0, 10)}`, secret, 300, NOW))).toBe("no_matching_signature");
    await verifyWebhook(body, `t=${NOW},v1=zz,v1=${good}`, secret, 300, NOW);
  });

  it("tolerates spaces around header parts", async () => {
    await verifyWebhook(body, ` t=${NOW} , v1=${sign(NOW, body)} `, secret, 300, NOW);
  });
});

describe("constructEvent", () => {
  it("verifies and returns the typed envelope", async () => {
    const event = await constructEvent(body, `t=${NOW},v1=${sign(NOW, body)}`, secret, 300, NOW);
    expect(event.id).toBe("0192");
    if (event.type === "invoice.paid") {
      expect(event.data.net_minor).toBe("930"); // narrowed: money stays a string
    } else {
      throw new Error("expected invoice.paid");
    }
  });

  it("refuses an unverified body before parsing it", async () => {
    expect(await reason(constructEvent(body, `t=${NOW},v1=${"00".repeat(32)}`, secret, 300, NOW))).toBe("no_matching_signature");
  });

  it("rejects a signed body that is not an event", async () => {
    for (const payload of ["not json", "[]", '{"type":1}', "null"]) {
      expect(await reason(constructEvent(payload, `t=${NOW},v1=${sign(NOW, payload)}`, secret, 300, NOW))).toBe("invalid_payload");
    }
  });
});
