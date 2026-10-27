import { afterEach, describe, expect, it } from "vitest";
import { ConnectionError, RateLimitError, ServerError, TimeoutError, ValidationError } from "../src/index";
import { apiError, clientFor, json, startServer, type FakeServer } from "./helpers";

let server: FakeServer | undefined;
afterEach(async () => {
  await server?.close();
  server = undefined;
});

describe("retries", () => {
  it("retries a network error and succeeds", async () => {
    server = await startServer((_req, res, n) => {
      if (n === 0) res.socket?.destroy();
      else json(res, 200, { id: "c1" });
    });
    const { client, delays } = clientFor(server);
    expect(await client.customers.getCustomer("c1")).toEqual({ id: "c1" });
    expect(server.requests).toHaveLength(2);
    expect(delays).toHaveLength(1);
  });

  it("gives up with ConnectionError once retries are spent", async () => {
    server = await startServer((_req, res) => res.socket?.destroy());
    const { client } = clientFor(server, { maxRetries: 2 });
    await expect(client.customers.getCustomer("c1")).rejects.toBeInstanceOf(ConnectionError);
    expect(server.requests).toHaveLength(3);
  });

  it("honours Retry-After on 429, for any method", async () => {
    server = await startServer((_req, res, n) => {
      if (n === 0) apiError(res, 429, "RESOURCE_EXHAUSTED", "common.rate_limited", "slow down", { "Retry-After": "3" });
      else json(res, 201, { id: "c1" });
    });
    const { client, delays } = clientFor(server);
    await client.customers.createCustomer({ external_id: "c", name: "C", email: "c@example.com" });
    expect(server.requests).toHaveLength(2);
    expect(delays).toEqual([3000]);
  });

  it("caps an absurd Retry-After", async () => {
    server = await startServer((_req, res, n) => {
      if (n === 0) apiError(res, 429, "RESOURCE_EXHAUSTED", "common.rate_limited", "x", { "Retry-After": "86400" });
      else json(res, 200, {});
    });
    const { client, delays } = clientFor(server, { maxRetryAfter: 10_000 });
    await client.customers.getCustomer("c");
    expect(delays).toEqual([10_000]);
  });

  it("surfaces a 429 as RateLimitError with retryAfter when retries are spent", async () => {
    server = await startServer((_req, res) => apiError(res, 429, "RESOURCE_EXHAUSTED", "common.rate_limited", "x", { "Retry-After": "7" }));
    const { client } = clientFor(server, { maxRetries: 1 });
    const err = await client.customers.getCustomer("c").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RateLimitError);
    expect((err as RateLimitError).retryAfter).toBe(7);
    expect(server.requests).toHaveLength(2);
  });

  it.each([502, 503, 504])("retries %i on a POST", async (status) => {
    server = await startServer((_req, res, n) => {
      if (n === 0) apiError(res, status, "UNAVAILABLE", "common.unavailable");
      else json(res, 201, { id: "c1" });
    });
    const { client } = clientFor(server);
    await client.customers.createCustomer({ external_id: "c", name: "C", email: "c@example.com" });
    expect(server.requests).toHaveLength(2);
  });

  it("retries a 500 for idempotent methods", async () => {
    server = await startServer((_req, res, n) => {
      if (n === 0) apiError(res, 500, "INTERNAL", "common.internal");
      else json(res, 200, {});
    });
    const { client } = clientFor(server);
    await client.customers.getCustomer("c");
    await client.taxRates.setTaxRates({});
    expect(server.requests.map((r) => r.method)).toEqual(["GET", "GET", "PUT"]);
  });

  it("does not retry a 500 on a POST without an idempotency key", async () => {
    server = await startServer((_req, res) => apiError(res, 500, "INTERNAL", "common.internal"));
    const { client } = clientFor(server);
    await expect(client.customers.createCustomer({ external_id: "c", name: "C", email: "c@example.com" })).rejects.toBeInstanceOf(ServerError);
    expect(server.requests).toHaveLength(1);
  });

  it("retries a 500 on a POST that carries an idempotency key, with the same key", async () => {
    server = await startServer((_req, res, n) => {
      if (n < 2) apiError(res, 500, "INTERNAL", "common.internal");
      else json(res, 201, {});
    });
    const { client } = clientFor(server);
    await client.customers.createCustomer({ external_id: "c", name: "C", email: "c@example.com" }, { idempotencyKey: "same" });
    expect(server.requests.map((r) => r.headers["idempotency-key"])).toEqual(["same", "same", "same"]);
  });

  it("generates one idempotency key for operations that take the header, stable across retries", async () => {
    server = await startServer((_req, res, n) => {
      if (n === 0) apiError(res, 503, "UNAVAILABLE", "common.unavailable");
      else if (n === 1) apiError(res, 500, "INTERNAL", "common.internal");
      else json(res, 201, {});
    });
    const { client } = clientFor(server);
    await client.paymentLinks.createPaymentLink({ kind: "adhoc", amount_minor: 1000n });
    const keys = server.requests.map((r) => r.headers["idempotency-key"]);
    expect(keys[0]).toMatch(/^sdk_/);
    expect(new Set(keys).size).toBe(1);
    expect(keys).toHaveLength(3);
    // a different call gets a different key
    await client.paymentLinks.createPaymentLink({ kind: "adhoc", amount_minor: 1000n });
    expect(server.requests[3]?.headers["idempotency-key"]).not.toBe(keys[0]);
  });

  it.each([400, 401, 403, 404, 409, 422])("never retries %i", async (status) => {
    server = await startServer((_req, res) => apiError(res, status, "X", "common.x"));
    const { client } = clientFor(server);
    await expect(client.customers.getCustomer("c")).rejects.toMatchObject({ status });
    expect(server.requests).toHaveLength(1);
  });

  it("backs off exponentially with jitter, capped", async () => {
    server = await startServer((_req, res) => apiError(res, 503, "UNAVAILABLE", "common.unavailable"));
    const { client, delays } = clientFor(server, { maxRetries: 4, initialRetryDelay: 100, maxRetryDelay: 500, random: () => 0 });
    await expect(client.customers.getCustomer("c")).rejects.toBeInstanceOf(ServerError);
    // random()=0 gives the floor of the jitter window: half of min(cap, initial * 2^n)
    expect(delays).toEqual([50, 100, 200, 250]);
    const full = clientFor(server, { maxRetries: 2, initialRetryDelay: 100, random: () => 1 });
    await full.client.customers.getCustomer("c").catch(() => undefined);
    expect(full.delays).toEqual([100, 200]);
  });

  it("per-call maxRetries overrides the client's", async () => {
    server = await startServer((_req, res) => apiError(res, 503, "UNAVAILABLE", "common.unavailable"));
    const { client } = clientFor(server);
    await client.customers.listCustomers(undefined, { maxRetries: 0 }).catch(() => undefined);
    expect(server.requests).toHaveLength(1);
  });

  it("times out per attempt and retries", async () => {
    server = await startServer((_req, res, n) => {
      if (n === 1) json(res, 200, { id: "ok" });
    });
    const { client } = clientFor(server, { timeout: 150 });
    expect(await client.customers.getCustomer("c")).toEqual({ id: "ok" });
    expect(server.requests).toHaveLength(2);
  });

  it("raises TimeoutError when every attempt times out", async () => {
    server = await startServer(() => undefined);
    const { client } = clientFor(server, { timeout: 150, maxRetries: 1 });
    await expect(client.customers.getCustomer("c")).rejects.toBeInstanceOf(TimeoutError);
    expect(server.requests).toHaveLength(2);
  });

  it("keeps validation errors typed", async () => {
    server = await startServer((_req, res) => apiError(res, 400, "INVALID_ARGUMENT", "plans.customer.invalid"));
    const { client } = clientFor(server);
    await expect(client.customers.createCustomer({ external_id: "", name: "", email: "" })).rejects.toBeInstanceOf(ValidationError);
  });
});
