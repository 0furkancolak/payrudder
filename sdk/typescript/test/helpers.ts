import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { Payrudder, type PayrudderOptions } from "../src/index";

export interface Recorded {
  method: string;
  url: string;
  headers: IncomingMessage["headers"];
  body: string;
}

export type Handler = (req: Recorded, res: ServerResponse, n: number) => void;

export interface FakeServer {
  url: string;
  requests: Recorded[];
  close(): Promise<void>;
}

/** A local HTTP server. `handler` gets the 0-based index of the request so tests can script a sequence. */
export async function startServer(handler: Handler): Promise<FakeServer> {
  const requests: Recorded[] = [];
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const rec: Recorded = { method: req.method ?? "", url: req.url ?? "", headers: req.headers, body: Buffer.concat(chunks).toString("utf8") };
      requests.push(rec);
      handler(rec, res, requests.length - 1);
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${port}`,
    requests,
    close: () =>
      new Promise<void>((resolve) => {
        server.closeAllConnections();
        server.close(() => resolve());
      }),
  };
}

export function json(res: ServerResponse, status: number, body: unknown, headers: Record<string, string> = {}): void {
  res.writeHead(status, { "Content-Type": "application/json", ...headers });
  res.end(JSON.stringify(body));
}

export function apiError(
  res: ServerResponse,
  status: number,
  code: string,
  key: string,
  message = "boom",
  headers: Record<string, string> = {},
): void {
  json(res, status, { error: { code, key, message, details: [{ field: "x", description: "bad" }], request_id: "req_body" } }, headers);
}

/** A client wired to a fake server, with a recorded (and instant) backoff. */
export function clientFor(server: FakeServer, options: PayrudderOptions = {}): { client: Payrudder; delays: number[] } {
  const delays: number[] = [];
  const client = new Payrudder({
    apiKey: "qk_test_abcd1234_secret",
    baseUrl: server.url,
    sleep: (ms) => {
      delays.push(ms);
      return Promise.resolve();
    },
    random: () => 1,
    ...options,
  });
  return { client, delays };
}
