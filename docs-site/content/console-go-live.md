# Console guide: going live

Every app has a sandbox and a live environment. The sandbox is for building against; live is where real customers are charged. This page walks through the **Go live** page that switches live on, and the tool that copies your sandbox configuration to live. The concepts are in [Test mode](test-mode.html).

It was written from the console's own texts (every name in bold is a string of the English console) and its pages' code, not by following it on a screen with a person, and nothing here was done against a real provider account. Where the console differs, the console is right.

## The state live starts in

**The live environment starts switched off: no live keys, no live webhooks, no real charges.** Until it is switched on it has no figures (**Live shows no figures until it is active.**), creating a live API key is refused with `plans.api_key.tenant_not_active`, and your sandbox keeps working in the meantime.

## The checklist

The **Go live** page (under the app's settings) lists five steps, each saying what is missing and taking you to where you fix it. The header counts how many are done.

1. **Confirm the owner's e-mail address**: open the link in the message the console sent, or ask for a new one from the banner.
2. **Complete the legal profile (name, country, tax id)**: **Your legal name, address and tax id are printed on every live invoice.** The **Billing profile** form on the same page asks for the **Legal name**, the address, the **Tax id** (the form says whether the seller's country requires it for invoices; spaces and dashes are ignored) and an optional **Tax office (optional)**. The **Language of invoices and mails** is used for a customer who has no language of their own. Save with **Save billing profile**.
3. **Connect a payment provider in live mode**: the **Payment provider** card. **Keys are typed once and stored encrypted. The provider is asked to accept them before they count.** Choose a provider (Stripe, Paddle, iyzico or bank transfer), type its keys (**Secret keys are not shown again after you save. Use live keys, not test keys.**) and press **Check and connect**; a bank transfer needs only bank details (**Save bank details**). The provider guides say what each one needs: [Stripe](provider-stripe.html), [Paddle](provider-paddle.html), [iyzico](provider-iyzico.html). A connected account shows as **Working** or **Not working**.
4. **Publish at least one plan in live**: **Copy your sandbox plans to live below, or create one in live.**
5. **Accept the terms of service**: **I accept the terms of service for this app's live environment.** **Only the owner of the account can switch live on.**

When every step is done, an owner presses **Switch live on**; before that the button says **Finish the other steps first.** After it, the page says **Your live environment is active.** A live environment that was suspended, frozen or banned shows **Your live environment is on hold (suspended, frozen or banned). Contact support to have it reinstated.**

## Copying the sandbox configuration to live

**Copy sandbox configuration to live** saves rebuilding by hand: **Meters, plans and prices, tax rates, coupons, mail settings, webhook endpoints and the texts of your agreements. Never customers, usage, invoices, payments or acceptances. It only adds: anything that already exists in live and differs is left alone.** Only owners and administrators can use it.

1. **Preview the changes**: nothing is written. The preview lists, per family (meters, plans, tax rates, coupons, e-mail, webhooks, contracts), what is **Add**ed, what is the **Same** and what is a **Conflict**. **A conflict is an object that exists in live with different settings. It is left as it is; change it in live by hand if you want them to match.**
2. **Add {n} to live** applies exactly the changes shown. If the configuration changed in the meantime, you are asked to preview again.
3. **A new signing secret was made for each webhook endpoint below. It is shown only now; the sandbox's secret was not copied.** Give the new secrets to your live receivers.

Running it again changes nothing, because objects are matched by their code.

## After going live

- Make a live API key on the app's keys page (a `qk_live_` key; see [Authentication](authentication.html)) and change the key and the address in your code. Live requires an `idempotency_key` on every usage event ([Idempotency](idempotency.html)).
- Create the live webhook endpoint ([Webhook endpoints](console-webhooks.html)) with an `https` address.
- Try one small real payment before you send customers there. Nothing in this repository has been run against a live provider account.

The **Sandbox tools** page resets the sandbox (**Deletes the sandbox's customers, subscriptions, usage, invoices and payments. Plans, prices, meters, tax rates, coupons, webhook endpoints, mail settings and API keys stay. Live is never touched.**) and lists the payment scenarios.
