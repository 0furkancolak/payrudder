#!/usr/bin/env node
import type { AddressInfo } from "node:net";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { createApi } from "./api.js";
import { createHttpServer } from "./http.js";
import { createServer } from "./server.js";

// PAYRUDDER_API_KEY        the key the tools act with (qk_test_... for the sandbox, qk_live_... for live); its permissions are the tools' permissions (stdio)
// PAYRUDDER_BASE_URL       the API address (default http://localhost:42800)
// PAYRUDDER_MCP_WRITES     1 to also offer the tools that write (create a customer, send usage events)
// PAYRUDDER_MCP_HTTP_PORT  set it to serve streamable HTTP on /mcp instead of stdio; each caller sends its own key as a bearer token, so PAYRUDDER_API_KEY is not used
// PAYRUDDER_MCP_HTTP_HOST  the address to listen on in that mode (default 127.0.0.1: put a TLS-terminating proxy in front before listening on anything else)
const baseUrl = process.env.PAYRUDDER_BASE_URL ?? "http://localhost:42800";
const writes = process.env.PAYRUDDER_MCP_WRITES === "1";
const httpPort = process.env.PAYRUDDER_MCP_HTTP_PORT;

if (httpPort !== undefined) {
  const port = Number(httpPort);
  if (!Number.isInteger(port) || port < 0 || port > 65535) {
    console.error("payrudder-mcp: PAYRUDDER_MCP_HTTP_PORT must be a port number.");
    process.exit(1);
  }
  const host = process.env.PAYRUDDER_MCP_HTTP_HOST ?? "127.0.0.1";
  const http = createHttpServer({ baseUrl, allowWrites: writes });
  http.listen(port, host, () => {
    console.error(`payrudder-mcp: serving streamable HTTP on http://${host}:${(http.address() as AddressInfo).port}/mcp (${writes ? "reads and writes" : "read only"}, the caller's key in every request)`);
  });
} else {
  const apiKey = process.env.PAYRUDDER_API_KEY;
  if (!apiKey) {
    console.error("payrudder-mcp: set PAYRUDDER_API_KEY (a qk_test_ or qk_live_ key).");
    process.exit(1);
  }
  const server = createServer({ api: createApi({ baseUrl, apiKey }), allowWrites: writes });
  await server.connect(new StdioServerTransport());
  console.error(`payrudder-mcp: ready (${writes ? "reads and writes" : "read only"}, ${apiKey.startsWith("qk_live_") ? "LIVE" : "sandbox"} key)`);
}
