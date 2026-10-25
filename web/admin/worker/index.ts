import { handle, type Env } from "./proxy";

// Entry of the console Worker (wrangler.jsonc: main). Static files are served by the assets binding; only the API paths and /env.js
// reach this code (assets.run_worker_first), so the console and the API share one origin and the session cookie needs no CORS.
export default {
  fetch: (request: Request, env: Env) => handle(request, env),
} satisfies ExportedHandler<Env>;
