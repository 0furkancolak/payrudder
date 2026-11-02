# Tax and e-invoices

What Payrudder does about tax and legal invoices, what it leaves to you, and where the legal e-documents of Turkey (and Peppol) fit. **Payrudder is not a tax engine.** It applies the rates you give it and records the tax data you give it; whether those are right for your sales is yours to decide, with your accountant.

## Who works out the tax

- **Subscriptions Payrudder runs** (payment through Stripe, iyzico or a bank transfer): Payrudder adds the tax you configured. `PUT /v1/tax-rates` sets a rate per customer country, as basis points (`2000` is 20 %), plus one rate with an empty country for every other customer. The rate is added **on top of** the price (exclusive). Payrudder does not work out where a sale is taxed, a place of supply, a threshold or an exemption, and it prints no reverse-charge or one-stop-shop wording, because the lines do not say which treatment applied.
- **Subscriptions a provider runs** ([Paddle](provider-paddle.html), a merchant of record): the provider works out and remits the tax, and Payrudder adds none.
- **A credit note** gives back the tax of the invoice it corrects; `tax_minor` on the credit note says how much.

## The customer's tax data

On the customer: `customer_type` (`individual` or `business`), `tax_id`, `tax_office` (Turkey) and `e_invoice_registered`, set with `PUT /v1/customers/{external_id}/tax`, and a country (`PUT /v1/customers/{external_id}/address`).

- The id is **checked locally** against the shape of a Turkish TCKN (individual) or VKN (business) and of a European VAT id. This is a checksum or shape check, **not a lookup**: no VIES, no query to the Turkish revenue administration.
- An id that does not look right is **stored all the same**, and the customer comes back with `tax_id_warning` set to what is odd (`tr_individual`, `tr_business`, `tr_any`, `tr_is_tckn`, `tr_is_vkn`, `eu_vat`). It is a hint, never a refusal. `tax_id_validated_at` is set only for an id that passed.
- The seller's own legal data (legal name, tax id, tax office, MERSIS and trade registry numbers, address) is edited in the console on the e-invoice page and appears on invoices and e-documents.

## Invoice documents

The invoice PDF is in the customer's language: English, Turkish, German, French and Spanish are laid out, and **an Arabic customer's PDF gets the English layout** (the PDF cannot yet shape Arabic). The hosted invoice page is available in all six languages. Amounts and dates follow the language's conventions and amounts carry the ISO currency code. Each party's tax id is labelled for what it is (a Turkish VKN or TCKN, a European VAT id) and printed with the tax office.

For a Turkish seller whose tenant issues e-documents, the invoice says that the legal e-Fatura or e-Arşiv document is issued separately by the integrator, with its own number. Nothing else legal is printed for any country.

## Legal e-documents (Turkey, Peppol)

When a tenant has an e-invoice integrator connected, each finalized invoice and credit note is turned into a legal e-document and sent to the integrator; the answer, the legal number and the files are kept with it.

- **Channels:** `e-fatura` and `e-arsiv` (Turkey) and `peppol`, rendered as UBL 2.1. The channel is chosen per document from the buyer (in Turkey, whether the buyer is registered for e-fatura).
- **Integrators:** Nilvera, or a **custom integrator** of your own that receives each signed document at an address you give. The sandbox always uses a built-in simulator and a sandbox document never leaves Payrudder.
- **The buyer by customer type.** An e-arşiv document without a buyer tax id is issued for an individual (or a customer whose type nobody entered) under the placeholder consumer number `11111111111`. A **business is refused** (`einvoice.buyer.incomplete`) and never given the placeholder: enter its tax id. Whether the authority also requires the tax office, and rejects a business under the placeholder, is **UNVERIFIED**; the refusal is the cautious reading.
- A failed document is retried by Payrudder when the integrator could not be reached, and shown with the integrator's reason when it refuses it for good (a wrong credential, a tax id the integrator does not know).
- Read them with `GET /v1/e-invoices` and `GET /v1/e-invoices/{id}`; `GET /v1/e-invoices/{id}/files/{kind}` returns a link (valid five minutes) to the UBL XML or the PDF.

## What has not been verified

- The documents are checked against the official UBL 2.1 schema, the EN 16931 and Peppol BIS Billing 3.0 schematron and the UBL-TR schema and schematron published for Turkey, with a short list of known gaps. **Passing that is not acceptance by a tax authority or a Peppol access point.**
- No adapter has run against an integrator's test environment: **no transcript of a real e-document exists yet**.
- No accountant or lawyer has read this page, the invoice wording or the buyer rules. Check what an invoice must carry in your country before you rely on it.
