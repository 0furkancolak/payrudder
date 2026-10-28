import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { ApiError, type Api } from "./api.js";
import { TOOLS } from "./tools.js";

/** The longest answer handed to the model, in characters; a longer one is cut and says so (the model can narrow the call or ask for the next page). */
export const MAX_REPLY = 60_000;

export interface ServerOptions {
  api: Api;
  /** Offer the tools that write. Off unless the person who starts the server turns it on. */
  allowWrites?: boolean;
}

export function createServer(o: ServerOptions): McpServer {
  const server = new McpServer({ name: "payrudder", version: "0.0.0" }, { instructions: "Tools over a Payrudder account (metered billing). Amounts are in minor units (cents) as text. Use next_page_token to page." });
  for (const t of TOOLS) {
    if (t.write && !o.allowWrites) continue;
    server.registerTool(
      t.name,
      {
        title: t.title,
        description: t.description,
        inputSchema: t.input,
        annotations: { readOnlyHint: !t.write, destructiveHint: false, idempotentHint: !t.write, openWorldHint: false },
      },
      async (args) => {
        try {
          let text = JSON.stringify(await t.run(args as Record<string, unknown>, o.api), null, 2) ?? "null";
          if (text.length > MAX_REPLY) text = `${text.slice(0, MAX_REPLY)}\n… cut: the answer is longer than ${MAX_REPLY} characters. Narrow the call or read the next page.`;
          return { content: [{ type: "text" as const, text }] };
        } catch (e) {
          const msg = e instanceof ApiError ? `${e.key}: ${e.message} (HTTP ${e.status})` : e instanceof Error ? `${e.name}: ${e.message}` : String(e);
          return { isError: true, content: [{ type: "text" as const, text: msg }] };
        }
      },
    );
  }
  return server;
}
