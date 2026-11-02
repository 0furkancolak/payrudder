# iyzico

iyzico takes Turkish cards and local methods. Payrudder runs the subscription (the renewal, the retries, the receipts) and **asks iyzico to charge** a saved card or sends the customer to iyzico's payment page.

This page is written from the adapter's code and from the compatibility matrix that records what iyzico's public documentation said (read on 2026-10-29 and 2026-11-02). **Nothing here has run against an iyzico account**, not even a sandbox one; the last section lists what that leaves open.

## What you need

From the iyzico merchant panel:

| Setting | Where |
|---|---|
| API key and secret key | *Settings, Company Settings, API keys*: the live pair. The secret key is shown once there |
| Buyer details | The person or company that appears as the buyer on charges: name, surname, tax id (TCKN or VKN), e-mail, address, city, country, postcode, the **fixed public IP address** your business sends requests from (iyzico ties the keys to it) and optionally a mobile number |

The console prefills the buyer from your billing profile. A sandbox key starts with `sandbox-`.

## Connect it

In the console open the app, then **Integrations**, **Payments**, **iyzico**, and enter the keys and the buyer details (only an owner can connect). The keys are checked with iyzico before they are stored; the sandbox has its own button ("Test mode") and takes the keys of the iyzico sandbox panel.

**Notifications.** iyzico posts payment notifications to the address set in the panel under *Merchant Settings, Merchant Notifications* (HTTPS). Payrudder's endpoint is `/webhooks/iyzico` on your deployment's public address. iyzico sends the first notification 10 to 15 seconds after a payment and repeats a failed one every 15 minutes, **three times at most**.

## How it works

- **The buyer is the customer.** iyzico vets who pays, so a charge sends the customer's own record: name, e-mail, address, tax id and a mobile number in international form (`+90…`; the API refuses a national spelling). Nothing is made up: a field the customer record lacks is taken from the account's own buyer details, and when neither has it the charge is declined as incomplete (nothing is charged, the retries read the customer again).
- **A payment under review** (iyzico's `fraudStatus` 0) is pending, not paid; the notification settles it. A rejected one is a decline.
- **The payment page.** A customer can pay on an iyzico checkout form: iyzico returns the customer to a Payrudder address with a token, and Payrudder asks iyzico for the result with that token before it believes anything.
- **Currencies:** TRY, USD, EUR, GBP and CHF.
- **No card numbers.** Putting a new card into iyzico's storage needs the card number to pass through Payrudder's servers, which is **not built**. A card is saved through iyzico's own page.

## What has not been verified

No iyzico account was used. **UNVERIFIED**, in particular:

- the **V3 signature** (`X-IYZ-SIGNATURE-V3`) is checked when the header is there, but iyzico documents that the feature "must be enabled on your account" (by writing to iyzico), and until it is, a notification is believed only because Payrudder asks iyzico about the payment itself;
- whether iyzico's payment-detail call returns checkout-form payments, and whether such a payment can be found by its conversation id;
- from which addresses iyzico sends notifications (not documented);
- whether BKM Express is still offered.

Run your first payments in iyzico's sandbox and read the result in the console before you go live.
