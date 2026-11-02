import { Payrudder } from "@payrudder/sdk";

// PAYRUDDER_API_KEY (qk_test_... for the sandbox) and PAYRUDDER_BASE_URL are read from the environment.
const payrudder = new Payrudder();

const [result] = await payrudder.events.ingest([
  {
    customer_external_id: "cus_42",
    meter_code: "api_calls",
    quantity: "1",
    event_time: new Date(),
    // Name the thing that happened (a request id, a job id) so a retry never counts twice. A live key must send one.
    idempotency_key: "request-8f2c",
  },
]);

if (result?.status === "rejected") {
  console.error(`rejected: ${result.error_code}: ${result.error_message}`);
  process.exitCode = 1;
} else {
  console.log(result?.status, result?.event_id); // accepted | duplicate
}
