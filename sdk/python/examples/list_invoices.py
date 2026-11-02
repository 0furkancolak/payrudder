"""List a customer's paid invoices. The iterator follows next_page_token for you and yields invoices, not pages."""

from payrudder import Payrudder

client = Payrudder()

for invoice in client.invoices.iter_invoices(customer="cus_42", status="paid", page_size=100):
    # total_minor is an int in the currency's minor units (cents, kurus), never a float.
    print(invoice["number"], invoice["period_id"], invoice["total_minor"], invoice["currency"])
