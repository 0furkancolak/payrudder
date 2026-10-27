import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, SANDBOX, productGateway, signInAsAda } from "./product";

// The first-run wizard (phase 52): a new sign-up (no app, never started it) is taken through why Payrudder exists and the first setup: organisation, app,
// meter from a template, package with a price, a test event in the sandbox. Progress is kept per person, it can be skipped and resumed, and the
// "Getting started" card on the home ticks from the person's data.

/** The tenant API of the app's sandbox, as far as the wizard uses it: meters, packages, a customer, a subscription and an event, kept in memory. */
function sandboxApi(gw: MockGateway, opts: { rejectEvent?: boolean } = {}) {
  const made = { meters: [] as Record<string, unknown>[], plans: [] as { plan: Record<string, unknown>; version?: Record<string, unknown> }[], customers: [] as string[] };
  gw.on((c) => {
    if (!c.path.startsWith("/v1/")) return undefined;
    if (c.path === "/v1/meters" && c.method === "GET") return { body: { meters: made.meters } };
    if (c.path === "/v1/meters" && c.method === "POST") {
      made.meters.push({ ...c.body, version: 1 });
      return { status: 201, body: { ...c.body, version: 1 } };
    }
    if (c.path === "/v1/plans" && c.method === "GET") return { body: { plans: made.plans.map((p) => p.plan) } };
    if (c.path === "/v1/plans" && c.method === "POST") {
      made.plans.push({ plan: { id: `p-${made.plans.length}`, ...c.body, latest_version: 0 } });
      return { status: 201, body: made.plans.at(-1)?.plan };
    }
    const versions = /^\/v1\/plans\/([^/]+)\/versions(\/latest)?$/.exec(c.path);
    if (versions && c.method === "POST") {
      const p = made.plans.find((x) => x.plan.code === versions[1]);
      p!.version = { version: 1, ...c.body };
      p!.plan.latest_version = 1;
      return { status: 201, body: p!.version };
    }
    if (versions && c.method === "GET") return { body: made.plans.find((x) => x.plan.code === versions[1])?.version ?? {} };
    if (c.path === "/v1/customers" && c.method === "POST") {
      made.customers.push(String(c.body?.external_id));
      return { status: 201, body: c.body };
    }
    if (c.path === "/v1/subscriptions" && c.method === "POST") return { status: 201, body: { id: "s1", state: "SUBSCRIPTION_STATE_ACTIVE" } };
    if (c.path === "/v1/events" && c.method === "POST") {
      return opts.rejectEvent
        ? { status: 202, body: { results: [{ status: "rejected", error_code: "UNKNOWN_METER", error_message: "no such meter" }] } }
        : { status: 202, body: { results: [{ status: "accepted", event_id: "e1" }] } };
    }
    return undefined;
  });
  return made;
}

const wizard = (page: Page) => page.getByTestId("onboarding");

test.describe("the first-run wizard", () => {
  test("a new sign-up learns why Payrudder exists and sets up an app, a meter, a package and a test event", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway(), { apps: [], keys: [], accountName: "Ada Labs" });
    const made = sandboxApi(gw);
    await gw.install(page, { onboarded: false, tourSeen: false });
    await signInAsAda(page);

    // Why: the four things Payrudder does.
    await expect(wizard(page)).toBeVisible();
    // The tour of the home waits for the wizard to be over.
    await expect(page.getByTestId("tour")).toHaveCount(0);
    await expect(wizard(page).getByRole("heading", { name: "Bill for what your customers use" })).toBeFocused();
    const flow = wizard(page).getByTestId("onb-flow").getByRole("listitem");
    await expect(flow).toHaveCount(4);
    await expect(flow.nth(0)).toContainText("Meter usage");
    await expect(flow.nth(3)).toContainText("Get paid");
    await page.getByTestId("onb-start").click();

    // Organisation: the name can be changed; the currency and the time zone are for the app.
    await expect(wizard(page).getByRole("heading", { name: "Your organisation" })).toBeVisible();
    await expect(wizard(page).getByTestId("onb-steps")).toContainText("Organisation");
    await wizard(page).getByTestId("onb-org-name").fill("Lovelace Labs");
    await wizard(page).getByTestId("onb-continue").click();
    await expect.poll(() => state.accountName).toBe("Lovelace Labs");

    // First app.
    await expect(wizard(page).getByRole("heading", { name: "Your first app" })).toBeVisible();
    await expect(wizard(page).getByTestId("onb-app-name")).toHaveValue("My first app");
    await wizard(page).getByTestId("onb-app-name").fill("Docs bot");
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("heading", { name: "What do you measure?" })).toBeVisible();
    expect(gw.called("POST", "/app/v1/apps")[0]?.body).toMatchObject({ name: "Docs bot", currency: "USD" });

    // First meter, from a template.
    await wizard(page).getByTestId("template-tokens").click();
    await expect(wizard(page).getByTestId("template-tokens")).toHaveAttribute("aria-checked", "true");
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("heading", { name: "Your first package" })).toBeVisible();
    expect(made.meters[0]).toMatchObject({ code: "ai_tokens", aggregation: "AGGREGATION_SUM", value_property: "tokens", unit_label: "tokens" });
    expect(gw.called("POST", "/v1/meters")[0]?.headers["x-payrudder-tenant"]).toBe(SANDBOX);

    // First package: a fixed fee and a price per unit, in major units of the app's currency.
    await expect(wizard(page).getByTestId("onb-package-name")).toHaveValue("Starter");
    await expect(wizard(page).getByTestId("onb-unit-price")).toHaveValue("0.0004");
    await wizard(page).getByTestId("onb-fee").fill("19,90");
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("heading", { name: "Send a test event" })).toBeVisible();
    expect(gw.called("POST", "/v1/plans")[0]?.body).toEqual({ code: "starter", name: "Starter" });
    expect(gw.called("POST", "/v1/plans/starter/versions")[0]?.body).toEqual({ base_fee_minor: "1990", fees: [], prices: [{ meter_code: "ai_tokens", per_unit: { unit_price: "0.04" } }] });

    // The test event: a customer, its subscription, then the event.
    await wizard(page).getByTestId("onb-send").click();
    await expect(wizard(page).getByTestId("onb-event-sent")).toContainText("1000 tokens");
    expect(made.customers).toEqual(["cus_onboarding"]);
    expect(gw.called("POST", "/v1/subscriptions")[0]?.body).toMatchObject({ customer_external_id: "cus_onboarding", plan_code: "starter" });
    expect(gw.called("POST", "/v1/events")[0]?.body).toMatchObject({ events: [{ customer_external_id: "cus_onboarding", meter_code: "ai_tokens", quantity: "1000" }] });
    await wizard(page).getByTestId("onb-continue").click();

    // Done, with the next steps; the home then carries on with the checklist.
    await expect(wizard(page).getByRole("heading", { name: "You are all set" })).toBeVisible();
    await expect(wizard(page).getByTestId("onb-next").getByRole("link")).toHaveCount(4);
    await wizard(page).getByTestId("onb-finish").click();
    await expect(wizard(page)).toHaveCount(0);
    // The home's own tour starts now, the first time the page is quiet.
    await expect(page.getByTestId("tour-card")).toContainText("Welcome to Payrudder");
    await page.getByTestId("tour-skip").click();
    await expect(page.getByTestId("tour")).toHaveCount(0);
    const card = page.getByTestId("getting-started");
    // The app, the meter, the package and the test event are done; the fixture's live environment is active already, and there is no key yet.
    await expect(card.getByTestId("getting-started-count")).toHaveText("5 of 6 done");
    await expect(card.getByTestId("todo-app")).toHaveAttribute("data-done", "true");
    await expect(card.getByTestId("todo-meter")).toHaveAttribute("data-done", "true");
    await expect(card.getByTestId("todo-package")).toHaveAttribute("data-done", "true");
    await expect(card.getByTestId("todo-event")).toHaveAttribute("data-done", "true");
    await expect(card.getByTestId("todo-key")).toHaveAttribute("data-done", "false");
    await expect(card.getByTestId("todo-live")).toHaveAttribute("data-done", "true");
    await expect(card.getByTestId("getting-started-resume")).toHaveCount(0);
    await expect(card.getByTestId("todo-key").getByRole("link", { name: "Open" })).toHaveAttribute("href", new RegExp(`/app/apps/${APP}/keys`));

    // Hiding the card keeps it away, also after a reload.
    await card.getByTestId("getting-started-hide").click();
    await expect(card).toHaveCount(0);
    await page.reload();
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(page.getByTestId("getting-started")).toHaveCount(0);
  });

  test("it is resumable: closing the page half way and coming back continues at the same step", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    sandboxApi(gw);
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await page.getByTestId("onb-start").click();
    await wizard(page).getByTestId("onb-continue").click();
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("heading", { name: "What do you measure?" })).toBeVisible();

    await page.reload();
    await expect(wizard(page).getByRole("heading", { name: "What do you measure?" })).toBeVisible();
    // The app that was made is not made again when the person steps back and forward.
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("heading", { name: "Your first package" })).toBeVisible();
    await wizard(page).getByRole("button", { name: "Back" }).click();
    await wizard(page).getByRole("button", { name: "Back" }).click();
    await expect(wizard(page).getByTestId("onb-app-made")).toContainText("My first app is ready");
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("heading", { name: "What do you measure?" })).toBeVisible();
    expect(gw.called("POST", "/app/v1/apps")).toHaveLength(1);
    expect(gw.called("POST", "/v1/meters")).toHaveLength(1);
  });

  test("it can be skipped at any step, and the checklist on the home then offers to continue where it stopped", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    sandboxApi(gw);
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await page.getByTestId("onb-start").click();
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("heading", { name: "Your first app" })).toBeVisible();
    await page.getByTestId("onb-skip").click();

    await expect(wizard(page)).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Create your first app" })).toBeVisible();
    const card = page.getByTestId("getting-started");
    await expect(card.getByTestId("getting-started-count")).toHaveText("0 of 6 done");
    // It does not come back by itself ...
    await page.reload();
    await expect(page.getByRole("heading", { name: "Create your first app" })).toBeVisible();
    await expect(wizard(page)).toHaveCount(0);
    // ... but the card brings it back, at the step it stopped at.
    await page.getByTestId("getting-started-resume").click();
    await expect(wizard(page).getByRole("heading", { name: "Your first app" })).toBeVisible();
  });

  test("a failed test event says why, and trying again does not make the customer twice", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    let reject = true;
    const made = sandboxApi(gw, { rejectEvent: true });
    gw.on((c) => (c.path === "/v1/events" && !reject ? { status: 202, body: { results: [{ status: "accepted", event_id: "e1" }] } } : undefined));
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await page.getByTestId("onb-start").click();
    await wizard(page).getByTestId("onb-continue").click();
    await wizard(page).getByTestId("onb-continue").click();
    await wizard(page).getByTestId("onb-continue").click();
    await wizard(page).getByTestId("onb-continue").click();
    await wizard(page).getByTestId("onb-send").click();
    await expect(wizard(page).getByRole("alert")).toContainText("no such meter");
    reject = false;
    await wizard(page).getByTestId("onb-send").click();
    await expect(wizard(page).getByTestId("onb-event-sent")).toBeVisible();
    expect(made.customers).toEqual(["cus_onboarding"]);
    expect(gw.called("POST", "/v1/customers")).toHaveLength(1);
    expect(gw.called("POST", "/v1/events")).toHaveLength(2);
  });

  test("a bad price is refused in words, before anything is sent", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    sandboxApi(gw);
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await page.getByTestId("onb-start").click();
    for (let i = 0; i < 3; i++) await wizard(page).getByTestId("onb-continue").click();
    await wizard(page).getByTestId("onb-fee").fill("twenty");
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("alert")).toContainText("Not an amount in USD");
    expect(gw.called("POST", "/v1/plans")).toHaveLength(0);
  });

  test("it never opens for somebody who has an app, nor for a viewer, and it is switched off by the support switch", async ({ page, context }) => {
    // Somebody with an app: the home, no wizard, no card.
    const withApp = productGateway(new MockGateway());
    await withApp.gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(wizard(page)).toHaveCount(0);
    await expect(page.getByTestId("getting-started")).toHaveCount(0);

    // A viewer with no app: nothing to create, so nothing to set up.
    const viewerPage = await context.newPage();
    const viewer = productGateway(new MockGateway(), { apps: [], role: "viewer" });
    await viewer.gw.install(viewerPage, { onboarded: false, tourSeen: true });
    await signInAsAda(viewerPage);
    await expect(viewerPage.getByText("An owner or administrator can create one.")).toBeVisible();
    await expect(viewerPage.getByTestId("onboarding")).toHaveCount(0);

    // The support switch.
    const quiet = await context.newPage();
    await quiet.addInitScript(() => localStorage.setItem("payrudder.noWelcome", "1"));
    await productGateway(new MockGateway(), { apps: [] }).gw.install(quiet, { onboarded: false, tourSeen: false });
    await signInAsAda(quiet);
    await expect(quiet.getByRole("heading", { name: "Create your first app" })).toBeVisible();
    await expect(quiet.getByTestId("onboarding")).toHaveCount(0);
    await expect(quiet.getByTestId("tour")).toHaveCount(0);
  });

  test("it speaks the person's language", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    // The wizard has its own language menu, since it covers the page's.
    await wizard(page).getByTestId("language-switcher").click();
    await page.getByTestId("lang-tr").click();
    await expect(wizard(page).getByRole("heading", { name: "Müşterilerinizin kullandığı kadar faturalayın" })).toBeVisible();
    await expect(wizard(page).getByTestId("onb-start")).toContainText("Hesabımı kur");
  });
});
