# Console guide: invoices and payments

What the console shows of the invoices Payrudder makes, the payments that settle them, and the links a customer pays through. The API is in the [reference](api/invoices.html); how an invoice is made and paid is in the [usage-based billing tutorial](tutorial-usage-billing.html) and [Let a customer pay](tutorial-payments.html).

It was written from the console's own texts (every name in bold is a string of the English console) and its pages' code, not by following it on a screen with a person. Where they differ, the console is right.

## Invoices

In the sidebar, under **Customers and billing**, **Invoices**: **What this environment billed. Open one to see its lines and download the PDF.** An invoice appears when a billing period closes (**No invoices yet. They appear when a billing period closes.**); before it has a number it is shown as **Draft**.

The list has a search (number, customer id, period), a **Status** filter (**draft**, **open**, **paid**, **uncollectible**, **void**) and a date range, which starts on the current month. The columns are **Number**, **Customer**, **Period**, **Total** (in the invoice's own currency), **Status** and **Created**. Rows come a page at a time (**Previous** and **Next**). Opening a row shows the invoice in a sheet that you can step through with the arrows without going back to the list.

What each status means:

- **draft**: made, not finalized.
- **open**: finalized and waiting for the money. A payment that fails is retried by the schedule on the [Pricing](console-pricing.html) page.
- **paid**: settled in full.
- **uncollectible**: the retries ran out.
- **void**: cancelled, not owed.

### One invoice

The sheet shows the number, the customer, the **Period**, the total and the status, and **Open PDF** (a link to the PDF that lasts a few minutes). Under **Lines** each line has a **Description**, a **Service period**, a **Quantity** and an **Amount**, then the **Total**. A line is the fixed fee of the package, usage per meter, late usage for a closed period, a correction, a coupon, or tax.

If the lines come to less than nothing, the invoice is not charged a negative total: the sheet says the amount **was added to the customer's credit balance**.

Money received outside Payrudder (a bank transfer, cash) and recorded against the invoice is listed as **Payments recorded by hand**, with the **Method**, **Amount**, **Received** date, **Reference**, **Note** and who recorded it. Recording one, confirming or rejecting a bank transfer, issuing a credit note and the saga timeline of an invoice are operator tools; in the app's own console they are not on this sheet. Credit notes that exist are read with `GET /v1/credit-notes` (the tenant API does not make them), and the e-invoice documents fetched with `GET /v1/e-invoices/{id}/files/{kind}` (see [Tax and e-invoices](tax-and-einvoices.html)).

**Related** lists what hangs off the invoice: its customer, the payment attempts made for it, its credit notes and the customer's subscriptions. Each row opens that record's sheet.

### In the sandbox: simulate a payment

An invoice of the sandbox has **Simulate payment**: **Run this invoice's payment again as one of the scenarios. The customer keeps the scenario as their payment method.** The scenarios are described on the **Sandbox tools** page and in [Test mode](test-mode.html): the charge succeeds, every attempt is declined, the first attempt is declined and the retry succeeds, the provider fails twice and then succeeds, or the provider does not answer in time. No real provider is called.

## Payments

**Payments**, next to Invoices: **Charges and refunds, newest first. Each payment shows the outcome it settled on.** The list can be filtered by status and type (**Charge** or **Refund**), and **View invoice** opens the invoice a payment belongs to. The page shows the **Latest 1000 ledger entries**.

## Payment links

**Payment links**: **A link the customer opens without signing in to pay.** **Anyone who has the link can pay; it stops working when it expires or is revoked.** A link is one of three **Kind**s:

- **Open invoice**: pays an invoice that is finalized and unpaid (**Invoice id**).
- **Fixed amount**: an amount, a currency and **What it is for**. **The invoice is made, and numbered, when the customer presses Continue on the page.**
- **Credit pack**: sells a credit pack (see the credits page).

Further fields: the **Customer id (optional)**, **Valid for (days)** (a whole number from 1 to 365), **Single use (one paid invoice)** and an optional **Promotion code (optional)**; the payer gets its discount without typing it and may type another. **Copy the link now: it is shown once and cannot be read back.** The list shows each link's **Status** (**Active**, **Paid**, **Expired**, **Revoked**), its **Uses / paid** and its expiry. **Revoke** stops a link at once; **Unpaid invoices made from it are voided.**

The same links are made with `POST /v1/payment-links` and, for an open invoice, `POST /v1/invoices/{id}/links`.
