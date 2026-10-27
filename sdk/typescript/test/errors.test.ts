import { afterEach, describe, expect, it } from "vitest";
import {
  AuthenticationError,
  ConflictError,
  NotFoundError,
  PayrudderError,
  PermissionError,
  RateLimitError,
  ServerError,
  ValidationError,
} from "../src/index";
import { apiError, clientFor, startServer, type FakeServer } from "./helpers";

let server: FakeServer | undefined;
afterEach(async () => {
  await server?.close();
  server = undefined;
});

describe("error mapping", () => {
  it.each([
    [400, ValidationError],
    [401, AuthenticationError],
    [403, PermissionError],
    [404, NotFoundError],
    [409, ConflictError],
    [422, ValidationError],
    [429, RateLimitError],
    [500, ServerError],
    [503, ServerError],
  ] as const)("maps %i to its class", async (status, Class) => {
    server = await startServer((_req, res) => apiError(res, status, "CODE", "billing.credit_note.nothing_to_refund", "nothing to refund"));
    const { client } = clientFor(server, { maxRetries: 0 });
    const err = await client.invoices.getInvoice("0192").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Class);
    expect(err).toBeInstanceOf(PayrudderError);
    const e = err as PayrudderError;
    expect(e.status).toBe(status);
    expect(e.code).toBe("CODE");
    expect(e.key).toBe("billing.credit_note.nothing_to_refund");
    expect(e.message).toBe("nothing to refund");
    expect(e.details).toEqual([{ field: "x", description: "bad" }]);
    expect(e.requestId).toBe("req_body");
  });

  it("prefers the request id header over the body", async () => {
    server = await startServer((_req, res) => apiError(res, 404, "NOT_FOUND", "common.not_found", "gone", { "X-Request-Id": "req_header" }));
    const { client } = clientFor(server);
    const e = (await client.invoices.getInvoice("0192").catch((x: unknown) => x)) as PayrudderError;
    expect(e.requestId).toBe("req_header");
    expect(e.name).toBe("NotFoundError");
  });

  it("copes with a non-JSON error body", async () => {
    server = await startServer((_req, res) => {
      res.writeHead(502, { "Content-Type": "text/html" });
      res.end("<html>Bad gateway</html>");
    });
    const { client } = clientFor(server, { maxRetries: 0 });
    const e = (await client.invoices.getInvoice("0192").catch((x: unknown) => x)) as PayrudderError;
    expect(e).toBeInstanceOf(ServerError);
    expect(e.status).toBe(502);
    expect(e.code).toBeUndefined();
    expect(e.message).toContain("Bad gateway");
    expect(e.body).toBe("<html>Bad gateway</html>");
  });
});
