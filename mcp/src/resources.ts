import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";

/**
 * The OpenAPI document of the tenant API and the public guides, as MCP resources, so an assistant can read how the API works before it calls a tool. scripts/generate.mjs
 * writes them (assets/index.json lists them); the server reads them when it starts, so a build without assets simply offers none.
 */
interface Asset {
  uri: string;
  name: string;
  title: string;
  description: string;
  mimeType: string;
  file: string;
}

/** assets/ sits next to src/ and dist/, so the same path works from source and from the build. */
export const ASSETS_DIR = fileURLToPath(new URL("../assets/", import.meta.url));

export function registerResources(server: McpServer, dir: string = ASSETS_DIR): number {
  let index: Asset[];
  try {
    index = JSON.parse(readFileSync(`${dir}/index.json`, "utf8")) as Asset[];
  } catch {
    return 0;
  }
  for (const a of index) {
    server.registerResource(a.name, a.uri, { title: a.title, description: a.description, mimeType: a.mimeType }, (uri) => ({
      contents: [{ uri: uri.href, mimeType: a.mimeType, text: readFileSync(`${dir}/${a.file}`, "utf8") }],
    }));
  }
  return index.length;
}
