import { Payrudder, constructEvent, WebhookEventTypes, type Invoice } from "../src/index";

// Compile-only: keeps the README snippets honest.
export async function snippets(): Promise<void> {
  const payrudder = new Payrudder({ apiKey: "k" });
  await payrudder.customers.createCustomer({ external_id: "cus_42", name: "Ada", email: "ada@example.com" });
  await payrudder.events.ingest([{ customer_external_id: "cus_42", meter_code: "ai_tokens", quantity: "1500", event_time: new Date() }]);
  await payrudder.customers.getCustomerUsage("cus_42", { period: "2026-11" });
  await payrudder.subscriptions.cancelSubscription("id", { credit_unused: true });
  await payrudder.customers.setCustomerAddress("cus_42", { country: "TR", city: "Istanbul" });
  await payrudder.invoices.listInvoices({ customer: "cus_42", status: "paid" });
  for await (const customer of payrudder.paginate.listCustomers({ page_size: 200 })) console.log(customer.external_id);
  const event = await constructEvent("{}", "", "s");
  if (event.type === WebhookEventTypes.InvoicePaid) console.log(event.data.invoice_id, event.data.net_minor);
  const inv: Invoice = await payrudder.invoices.getInvoice("id");
  console.log(inv.total_minor satisfies string | undefined);
  await payrudder.paymentLinks.createPaymentLink({ kind: "adhoc", amount_minor: 1000n });
}
