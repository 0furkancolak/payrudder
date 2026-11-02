import { ConflictError, Payrudder } from "@payrudder/sdk";

const payrudder = new Payrudder();

try {
  const customer = await payrudder.customers.createCustomer({ external_id: "cus_42", name: "Ada Lovelace", email: "ada@example.com" });
  console.log("created", customer.external_id);
} catch (err) {
  // The external id is the customer's natural key: creating it twice answers 409 instead of making a second customer.
  if (!(err instanceof ConflictError)) throw err;
  console.log("cus_42 exists already");
}
