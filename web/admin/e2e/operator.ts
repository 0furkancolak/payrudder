import { expect, type Page } from "@playwright/test";

// The real-stack specs use the operator console the way a person does: they hold the session of a person whose address the gateway lists in
// PAYRUDDER_OPERATOR_EMAILS (the compose stack lists user1@example.com, the dev seed's first user). There is no token form. PAYRUDDER_OPERATOR_TOKEN stays what it
// always was, the machine credential the specs use to call the API directly.
//
// The session comes from the real sign-in routes, once per run: the dev seed's password if that person exists (`make dev`), else a one-time code read from
// Mailpit (a stack without the seed, such as CI's: the code creates the person and proves the address). The page is then opened with that session in the
// tab's storage, like a person who signed in a moment ago; the sign-in pages themselves are tested in signin-real.spec.ts and e2e-mock/signin.spec.ts.
const gateway = process.env.PAYRUDDER_GATEWAY_URL ?? "http://localhost:42800";
const mailpit = process.env.MAILPIT_URL ?? "http://localhost:42825";
export const operatorEmail = process.env.PAYRUDDER_OPERATOR_EMAIL ?? "user1@example.com";
export const operatorPassword = process.env.PAYRUDDER_OPERATOR_PASSWORD ?? "password123";

type Json = Record<string, any>; // eslint-disable-line @typescript-eslint/no-explicit-any

// Its own client address (the gateway counts the public sign-in routes per address and believes X-Forwarded-For from a private connection on the compose
// stack), so the specs that follow do not share its bucket.
const fromAddress = { "x-forwarded-for": "10.201.0.1" };

async function post(path: string, body: unknown): Promise<{ status: number; body: Json }> {
  const res = await fetch(gateway + path, { method: "POST", headers: { "content-type": "application/json", ...fromAddress }, body: JSON.stringify(body) });
  return { status: res.status, body: (await res.json().catch(() => ({}))) as Json };
}

/** The newest sign-in code mailed to an address after `since` (milliseconds), from Mailpit. */
export async function mailedCode(address: string, since: number): Promise<string> {
  for (let i = 0; i < 60; i++) {
    const res = await fetch(`${mailpit}/api/v1/search?query=${encodeURIComponent(`to:${address}`)}`);
    const list = ((await res.json()) as { messages?: { Subject: string; Created: string }[] }).messages ?? [];
    const mail = list.find((m) => /sign-in code/i.test(m.Subject) && new Date(m.Created).getTime() > since);
    const code = mail ? /(\d{6})/.exec(mail.Subject)?.[1] : undefined;
    if (code) return code;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`no sign-in code arrived for ${address}: is Mailpit at ${mailpit}? (a code is mailed at most once a minute per address and five a minute per client address: wait a minute and run again)`);
}

/** Follows the verification link a sign-up mailed to an address (the link's token is in the mail's text, after "#token="), as the person would. */
export async function verifyAddress(address: string, since: number): Promise<void> {
  for (let i = 0; i < 60; i++) {
    const res = await fetch(`${mailpit}/api/v1/search?query=${encodeURIComponent(`to:${address}`)}`);
    const list = ((await res.json()) as { messages?: { ID: string; Subject: string; Created: string }[] }).messages ?? [];
    const mail = list.find((m) => /confirm/i.test(m.Subject) && new Date(m.Created).getTime() > since);
    if (mail) {
      const text = ((await (await fetch(`${mailpit}/api/v1/message/${mail.ID}`)).json()) as { Text?: string }).Text ?? "";
      const token = /token=(qv_[A-Za-z0-9_-]+)/.exec(text)?.[1];
      if (token) {
        expect((await post("/auth/verify", { token })).status).toBe(200);
        return;
      }
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`no verification mail arrived for ${address}`);
}

let session: Promise<string> | undefined;

/** The session token of the operator user: signed in once and kept for the run. */
export function operatorSession(): Promise<string> {
  session ??= (async () => {
    const withPassword = await post("/auth/signin", { email: operatorEmail, password: operatorPassword });
    let signedIn = withPassword.status === 200 ? withPassword : undefined;
    if (!signedIn) {
      const since = Date.now() - 2000;
      expect((await post("/auth/otp/request", { email: operatorEmail })).status).toBe(202);
      signedIn = await post("/auth/otp/verify", { email: operatorEmail, code: await mailedCode(operatorEmail, since), account_name: "Operator" });
      expect(signedIn.status, JSON.stringify(signedIn.body)).toBe(200);
    }
    expect(signedIn.body.operator, `${operatorEmail} must be listed in PAYRUDDER_OPERATOR_EMAILS on the gateway and have a verified address`).toBe(true);
    return signedIn.body.token as string;
  })();
  return session;
}

/** Opens the operator console as the operator user: their session is in the tab's storage, as after signing in. */
export async function signInAsOperator(page: Page): Promise<void> {
  const token = await operatorSession();
  await page.addInitScript((t) => {
    window.sessionStorage.setItem("payrudder.sessionToken", t);
    window.sessionStorage.setItem("payrudder.operator", "1");
  }, token);
  await page.goto("/operator");
  await page.waitForURL("**/tenants");
  await expect(page.locator("main")).toBeVisible();
}
