import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";

// Bulk actions on the webhook endpoint list (docs/13-admin-ui.md, "Lists"): tick rows, act on all of them, the failure is named and stays ticked.

test.use({ viewport: { width: 1440, height: 900 } });

const ep = (id: string, url: string, active = true) => ({ id, url, description: "", event_types: ["invoice.paid"], active, create_time: "2026-10-20T10:00:00Z" });

test("ticked webhook endpoints are paused together, and one that cannot be changed is named and stays ticked", async ({ page }) => {
  const endpoints = [ep("e1", "https://a.example/hook"), ep("e2", "https://b.example/hook"), ep("e3", "https://c.example/hook", false)];
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.path === "/v1/webhook-endpoints" && c.method === "GET") return { body: { endpoints } };
    if (c.method === "PUT" && c.path === "/v1/webhook-endpoints/e2") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "plans.request.invalid", message: "no" } } };
    if (c.method === "PUT" && c.path.startsWith("/v1/webhook-endpoints/")) {
      const e = endpoints.find((x) => c.path.endsWith(x.id));
      if (e) e.active = c.body?.active === true;
      return { body: e };
    }
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/webhooks");
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await expect(page.getByText("3 selected")).toBeVisible();
  await page.getByTestId("bulk-pause").click();
  // The paused one is skipped, the second is refused, the first is paused.
  await expect(page.getByTestId("bulk-note")).toContainText("1 done, 1 not changed");
  await expect(page.getByTestId("bulk-note")).toContainText("https://b.example/hook");
  expect(gw.called("PUT", /\/v1\/webhook-endpoints\//).map((c) => c.path)).toEqual(["/v1/webhook-endpoints/e1", "/v1/webhook-endpoints/e2"]);
  expect(endpoints[0]?.active).toBe(false);
  // The one that failed (and the one that was not an active endpoint) stay ticked.
  await expect(page.getByText("2 selected")).toBeVisible();
});

// --- the selection bar for a screen reader and a keyboard (docs/plans/02, "Bulk"): a live region, Escape, Tab

test("the bar is announced politely, its actions follow in Tab order, and Escape clears it and puts the focus back on the header's tick", async ({ page }) => {
  const endpoints = [ep("e1", "https://a.example/hook"), ep("e2", "https://b.example/hook")];
  const gw = baseTenant(new MockGateway()).on((c) => (c.path === "/v1/webhook-endpoints" && c.method === "GET" ? { body: { endpoints } } : undefined));
  await gw.install(page);
  await openTenantPage(page, "/webhooks");
  const live = page.getByTestId("bulk-live");
  await expect(live).toHaveAttribute("aria-live", "polite");
  await expect(live).toHaveText("");

  // Ticked from the keyboard: the live region says how many, in words that are not the bar's own text.
  const head = page.getByRole("checkbox", { name: "Select all rows on this page" });
  await head.focus();
  await page.keyboard.press("Space");
  await expect(live).toContainText("2 rows selected");
  await expect(page.getByRole("region", { name: "Bulk actions" })).toBeVisible();

  // Tab goes through the actions in order and ends on the clear button.
  await page.getByTestId("bulk-pause").focus();
  await page.keyboard.press("Tab");
  await expect(page.getByTestId("bulk-resume")).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByTestId("bulk-delete")).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByTestId("bulk-clear")).toBeFocused();

  // Escape in the bar unticks everything, says so, and the focus is not lost with the bar.
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("bulk-bar")).toHaveCount(0);
  await expect(live).toHaveText("Selection cleared");
  await expect(head).toBeFocused();

  // Escape from a tick does the same.
  await page.keyboard.press("Space");
  await expect(page.getByTestId("bulk-bar")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("bulk-bar")).toHaveCount(0);
});

test("Escape that closes a popover, or is typed in a text box, does not clear the selection", async ({ page }) => {
  const endpoints = [ep("e1", "https://a.example/hook"), ep("e2", "https://b.example/hook")];
  const gw = baseTenant(new MockGateway()).on((c) => (c.path === "/v1/webhook-endpoints" && c.method === "GET" ? { body: { endpoints } } : undefined));
  await gw.install(page);
  await openTenantPage(page, "/webhooks");
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await expect(page.getByTestId("bulk-bar")).toBeVisible();

  await page.getByRole("button", { name: "Status", exact: true }).click();
  await expect(page.getByRole("listbox", { name: "Status" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("listbox", { name: "Status" })).toHaveCount(0);
  await expect(page.getByTestId("bulk-bar")).toBeVisible();

  await page.getByRole("searchbox").fill("a.example");
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("bulk-bar")).toBeVisible();
});

// --- select all N results (docs/13-admin-ui.md, "Selection")

const many = (n: number, from = 1) =>
  Array.from({ length: n }, (_, i) => ({ id: `id-${from + i}`, external_id: `cus_${String(from + i).padStart(2, "0")}`, name: `Customer ${from + i}`, email: `c${from + i}@example.com`, billing_currency: "TRY", create_time: "2026-10-05T10:00:00Z" }));

test("the page ticked offers every result; the bulk action asks first, with the count, and runs over all of them", async ({ page }) => {
  const all = many(23);
  const blocked = new Set<string>();
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: all.map((x) => (blocked.has(x.external_id) ? { ...x, block_time: "2026-10-28T10:00:00Z" } : x)), next_page_token: "" } };
    const m = /^\/v1\/customers\/(cus_\d+)\/block$/.exec(c.path);
    if (m && c.method === "POST") return blocked.add(m[1]!), { body: {} };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_01").waitFor();
  await expect(page.getByTestId("bulk-bar")).toHaveCount(0);
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await expect(page.getByTestId("bulk-count")).toHaveText("10 selected");
  await page.getByTestId("bulk-select-all").click();
  await expect(page.getByTestId("bulk-count")).toHaveText("23 selected");
  await expect(page.getByTestId("bulk-select-all")).toHaveCount(0);
  await expect(page.getByTestId("bulk-live")).toContainText("23 rows selected");

  await page.getByTestId("bulk-block").click();
  await expect(page.getByTestId("confirm-dialog")).toContainText("all 23 selected rows");
  await page.getByTestId("confirm-accept").click();
  await expect(page.getByTestId("bulk-note")).toContainText("23 done, 0 not changed");
  expect(gw.called("POST", /\/block$/)).toHaveLength(23);
});

test("a run on more than twenty rows shows how far it has come, done and not changed, while it runs, and a short one does not", async ({ page }) => {
  const all = many(23);
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: all, next_page_token: "" } };
    if (c.method === "POST" && c.path === "/v1/customers/cus_03/block") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "plans.request.invalid", message: "no" } } };
    if (c.method === "POST" && /^\/v1\/customers\/cus_\d+\/block$/.test(c.path)) return { body: {} };
    return undefined;
  });
  await gw.install(page);
  // The tenth request waits for the test, so the bar can be looked at in the middle of the run. (A route added later is asked first; fallback hands the call on.)
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => (release = resolve));
  await page.route("**/gw/v1/customers/cus_10/block", async (route) => {
    await gate;
    await route.fallback();
  });
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_01").waitFor();
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await page.getByTestId("bulk-select-all").click();
  await expect(page.getByTestId("bulk-count")).toHaveText("23 selected");
  await expect(page.getByTestId("bulk-progress")).toHaveCount(0);
  await page.getByTestId("bulk-block").click();
  await page.getByTestId("confirm-accept").click();

  // Nine rows are through, one of them refused; the tenth is on its way.
  await expect(page.getByTestId("bulk-progress-text")).toHaveText("8 of 23 done · 1 not changed");
  const bar = page.getByRole("progressbar", { name: "Progress of the bulk action" });
  await expect(bar).toHaveAttribute("aria-valuenow", "9");
  await expect(bar).toHaveAttribute("aria-valuemax", "23");
  await expect(bar).toHaveAttribute("aria-valuetext", "8 of 23 done · 1 not changed");
  release();
  await expect(page.getByTestId("bulk-note")).toContainText("22 done, 1 not changed");
  await expect(page.getByTestId("bulk-progress")).toHaveCount(0);
  // The refused row stays ticked.
  await expect(page.getByTestId("bulk-count")).toHaveText("1 selected");

  // Twenty rows or fewer: over before anybody could read it, so no panel.
  await page.getByTestId("bulk-clear").click();
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await page.getByTestId("bulk-block").click();
  await page.getByTestId("confirm-accept").click();
  await expect(page.getByTestId("bulk-note")).toContainText("9 done, 1 not changed");
  expect(await page.getByTestId("bulk-progress").count()).toBe(0);
});

test("a filter narrows what 'select all' takes, and the bar goes with a selection that fits one page", async ({ page }) => {
  const all = [...many(14), ...many(3, 50).map((x) => ({ ...x, country: "DE" }))];
  const gw = baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/customers" ? { body: { customers: all, next_page_token: "" } } : undefined));
  await gw.install(page);
  await openTenantPage(page, "/customers?country=DE");
  await page.getByTestId("customer-cus_50").waitFor();
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await expect(page.getByTestId("bulk-count")).toHaveText("3 selected");
  // Three results fit the page: there is nothing more to select.
  await expect(page.getByTestId("bulk-select-all")).toHaveCount(0);
});

test("a list the console holds one server page of loads the rest by its page tokens before it selects", async ({ page }) => {
  const first = many(12);
  const second = many(9, 13);
  const blocked = new Set<string>();
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") {
      const token = c.query.get("page_token");
      const wrap = (rows: typeof first) => rows.map((x) => (blocked.has(x.external_id) ? { ...x, block_time: "2026-10-28T10:00:00Z" } : x));
      return { body: token === "p2" ? { customers: wrap(second), next_page_token: "" } : { customers: wrap(first), next_page_token: "p2" } };
    }
    const m = /^\/v1\/customers\/(cus_\d+)\/block$/.exec(c.path);
    if (m && c.method === "POST") return blocked.add(m[1]!), { body: {} };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_01").waitFor();
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  // The server has more than the console holds, so the button cannot give a count yet.
  await expect(page.getByTestId("bulk-select-all")).toHaveText("Select all matching results");
  await page.getByTestId("bulk-select-all").click();
  await expect(page.getByTestId("bulk-count")).toHaveText("21 selected");
  expect(gw.called("GET", "/v1/customers").map((c) => c.query.get("page_token"))).toEqual([null, "p2"]);

  await page.getByTestId("bulk-block").click();
  await expect(page.getByTestId("confirm-dialog")).toContainText("21");
  await page.getByTestId("confirm-accept").click();
  await expect(page.getByTestId("bulk-note")).toContainText("21 done, 0 not changed");
});

test("when the rest of the list cannot be loaded nothing more is selected and the person is told", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") return c.query.get("page_token") ? { status: 503, body: { error: { code: "UNAVAILABLE", message: "down" } } } : { body: { customers: many(12), next_page_token: "p2" } };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_01").waitFor();
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await page.getByTestId("bulk-select-all").click();
  await expect(page.getByTestId("bulk-note")).toContainText("could not be loaded");
  await expect(page.getByTestId("bulk-count")).toHaveText("10 selected");
});
