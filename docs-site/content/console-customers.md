# Console guide: customers

How to find, add, change and block a customer in the console. This page is a guide to the screens; the API is in the [reference](api/customers.html) and the [usage-based billing tutorial](tutorial-usage-billing.html) shows the calls.

It was written from the console's own texts (every name in bold is a string of the English console) and its pages' code. It has not been checked by following it on a screen with a person, and the console changes faster than this page: where they differ, the console is right.

## Where it is

In the sidebar, under **Customers and billing**, the first item is **Customers**. Your app usually creates customers through the API (`POST /v1/customers`, with its own id as the customer's `external_id`); the console is for looking at them and for the odd one added or fixed by hand.

## The list

Search by name, e-mail or id (**Search name, e-mail, id…**). The filters are the country, the currency, the subscription (**Has an active subscription** or **No active subscription**) and the billing state (**Open invoices** or **Delinquent**). The columns are **Customer**, **Subscriptions**, **Currency**, **Country**, **Total spend**, **Created** and **Last payment**.

**Spend, balances and last payments are worked out from the newest invoices and payments.** On a customer with a long history they are a recent picture, not a ledger: the ledger is the invoices and payments themselves.

A row's menu has the same actions as the sheet and one more, **Quotas**: the customer's own limits, which override those of their package, shown with their usage in force.

Tick rows to act on several at once: **Export selected as CSV**, **Block** and **Lift the block**. A bulk action says how many rows it changed and how many it could not.

## Adding a customer

**New customer** opens a sheet with: **Customer id** (the id your app uses for them; it cannot be taken by another customer), **Name**, **E-mail**, **Billing currency** (left alone, it is the **Account's currency**), then the profile and the address.

The profile decides how the customer is addressed:

- **Language**: the language of their mails, receipts and payment pages. Left on **Browser's language**, the page follows the browser and a mail follows the account's default.
- **Type**: **Individual** or **Business (taxpayer)**; **Not specified** is allowed. The type matters to e-invoices (see [Tax and e-invoices](tax-and-einvoices.html)).
- **Mobile number**: **In international form, for example +90 532 123 45 67.** A number without its country code is refused.

The **Billing address** is a country, **State / region**, **City**, **Address line 1**, **Address line 2** and **Postal code**. The country of the address is the customer's country, which sets the tax rate used on their invoices (see [Pricing](console-pricing.html)).

Save with **Create customer**.

## The customer's sheet

Opening a row shows the customer's sheet. At the top, the customer's id with a copy button and the day the customer was created, then four figures: **Total spend**, **Open balance**, **Credit balance** and **Active subscriptions**. The sections are **Overview**, **Payments**, **Invoices**, **Subscriptions**, **Credits**, **Details** and **Agreements**.

Actions on the sheet:

- **New subscription**: subscribe the customer to a package.
- **Payment link**: make a link the customer opens to pay without signing in (see [Invoices and payments](console-invoices.html)).
- **Correct usage**: fix a quantity your app sent. **The event itself is not changed: the difference is added to the period it belongs to, and once that period is closed it is billed on the next invoice.** You give the meter, the idempotency key your app sent with the event (**Event key**), the quantity it should have been (0 cancels the event) and a reason. An event can be corrected for at least 45 days after it was sent. See also [Usage metering](usage-metering.html).
- **Ask for a card**: make the link to Stripe's own page where the customer saves a card for later charges, to send to them (**Return address**, `https` only). Nothing is charged, no card number passes through Payrudder, and **Payrudder collects and keeps no consent in this flow**: the sheet reminds you that Stripe's guide asks you to get and keep the customer's explicit agreement to be charged without them. When the customer has saved the card it becomes their payment method. The same link is made with `POST /v1/customers/{external_id}/payment-method-setup`. Not shown to a viewer.
- **Block**: see below.
- **Edit**: **Edit name and e-mail**, **Edit address** and **Tax data**.

**Upcoming invoice** shows what the open period would be invoiced if it were drafted now. **An estimate as of {when}, worked out the way the real invoice is. It moves with every event until the period closes, and a plan change, coupon or tax rate set later can change it.**

### The payment page

**Payment page** is the customer's own page, which shows their invoices, payments and credits, opened from a link. **Copy link** and **Open page** give it to the customer; **Regenerate link** makes a new one and the old ones stop working; **Revoke all** stops every link. The link is valid for a number of days that the page states. The same link can be made with `POST /v1/customers/{external_id}/portal-links`.

### Tax data

**Tax data** holds what e-invoices carry: **Tax id (VKN/TCKN or VAT id)**, **Tax office** and **Registered for e-fatura**. Save with **Save tax data**.

The form only hints. A number that does not look like the kind of number the customer should have (a TCKN for an individual in Turkey, a VKN for a business there, the shape and check digits of a VAT id for a business in the EU) gets a warning and **you can still save it**; the sheet's **Details** section then says **This number does not look right for this customer. Open the tax data to see why.** The checks are local: they do not look the number up at any registry.

## Blocking a customer

**Block** stops a customer from starting anything new. **No new subscription, plan change or purchase can be started for them. What they have now runs on.** The form asks:

- **Why**: **Fraud**, **Chargeback**, **Abuse**, **Their own request** or **Other**. Fraud and chargeback also stop them paying an invoice that is open.
- **Your note**: **Only you see it, never the customer.** Up to 500 characters.
- **Ends on**: leave it empty to block until you lift it; the customer can buy again from that day.
- **Also suspend their service**: while on, entitlement checks for this customer answer "not allowed" on every meter. **Usage you send is still accepted and counted.**

A blocked customer wears a **Blocked** badge. **Lift the block** removes it, and **Block history** lists every block and lift with who made it and when.
