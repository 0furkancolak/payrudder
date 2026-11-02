import { Payrudder } from "@payrudder/sdk";

// PAYRUDDER_API_KEY and PAYRUDDER_BASE_URL are read by the SDK when the client is made. It is made on first use, not when the module loads: `next build` loads every route without your settings.
let client: Payrudder | undefined;
export const payrudder = () => (client ??= new Payrudder());
