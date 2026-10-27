import { PayrudderResources } from "./generated/client-base";
import { HttpTransport, resolveConfig, type PayrudderOptions } from "./transport";

/**
 * Client of the Payrudder tenant API (`/v1/*`).
 *
 * ```ts
 * const payrudder = new Payrudder({ apiKey: "qk_test_..." });
 * const customer = await payrudder.customers.getCustomer("cus_42");
 * ```
 */
export class Payrudder extends PayrudderResources {
  constructor(options: PayrudderOptions = {}) {
    super(new HttpTransport(resolveConfig(options)));
  }
}
