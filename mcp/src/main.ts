#!/usr/bin/env node
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { createApi } from "./api.js";
import { createServer } from "./server.js";

// PAYRUDDER_API_KEY    the key the tools act with (qk_test_... for the sandbox, qk_live_... for live); its permissions are the tools' permissions
// PAYRUDDER_BASE_URL   the API address (default http://localhost:42800)
// PAYRUDDER_MCP_WRITES 1 to also offer the tools that write (create a customer, send usage events)
const apiKey = process.env.PAYRUDDER_API_KEY;
if (!apiKey) {
  console.error("payrudder-mcp: set PAYRUDDER_API_KEY (a qk_test_ or qk_live_ key).");
  process.exit(1);
}
const writes = process.env.PAYRUDDER_MCP_WRITES === "1";
const server = createServer({ api: createApi({ baseUrl: process.env.PAYRUDDER_BASE_URL ?? "http://localhost:42800", apiKey }), allowWrites: writes });
await server.connect(new StdioServerTransport());
console.error(`payrudder-mcp: ready (${writes ? "reads and writes" : "read only"}, ${apiKey.startsWith("qk_live_") ? "LIVE" : "sandbox"} key)`);
