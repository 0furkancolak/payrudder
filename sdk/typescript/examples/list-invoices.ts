import { Payrudder } from "@payrudder/sdk";

const payrudder = new Payrudder();

// The iterator follows next_page_token for you and yields invoices, not pages.
for await (const invoice of payrudder.paginate.listInvoices({ customer: "cus_42", status: "paid", page_size: 100 })) {
  // total_minor is a string: money is never a JavaScript number. Minor units are cents, kurus, ...
  console.log(invoice.number, invoice.period_id, invoice.total_minor, invoice.currency);
}
