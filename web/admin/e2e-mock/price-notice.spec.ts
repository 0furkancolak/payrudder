import { expect, test } from "@playwright/test";
import { MockGateway, signInAsOperator } from "./gateway";

// The operator's notice rules for price changes (plan 15): the default notice and the least notice per country, marked unverified and not legal advice; a save sends the
// whole table.

test.use({ viewport: { width: 1440, height: 900 } });

test("a failed read of the notice rules shows an error with a retry, not an empty form", async ({ page }) => {
  const gw = new MockGateway();
  gw.on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [] } } : undefined));
  // The last handler added is asked first.
  gw.on((c) => (c.method === "GET" && c.path === "/admin/v1/price-change-notice" ? { status: 503, body: { error: { code: "UNAVAILABLE", key: "", message: "the notice table is down" } } } : undefined));
  await gw.install(page);
  await signInAsOperator(page);
  const card = page.getByTestId("price-notice");
  await expect(card).toContainText("the notice table is down");
  await expect(card.getByRole("button", { name: "Try again" })).toBeVisible();
  await expect(card.getByTestId("price-notice-save")).toHaveCount(0);
});

test("the operator edits the default notice and the notice per country, and the whole table is sent", async ({ page }) => {
  const gw = new MockGateway();
  gw.on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [] } } : undefined));
  await gw.install(page);
  await signInAsOperator(page);
  const card = page.getByTestId("price-notice");
  await expect(card).toContainText("Unverified, not legal advice");
  await expect(card.getByTestId("price-notice-default")).toHaveValue("30");
  await expect(card.getByTestId("price-notice-country-0")).toHaveValue("*");
  await expect(card.getByTestId("price-notice-days-0")).toHaveValue("14");

  await card.getByTestId("price-notice-default").fill("45");
  await card.getByTestId("price-notice-add").click();
  await card.getByTestId("price-notice-country-1").fill("de");
  await card.getByTestId("price-notice-days-1").fill("60");
  await card.getByTestId("price-notice-note-1").fill("read by nobody");
  await card.getByTestId("price-notice-save").click();
  await expect.poll(() => gw.called("PUT", "/admin/v1/price-change-notice")).toHaveLength(1);
  expect(gw.called("PUT", "/admin/v1/price-change-notice")[0]?.body).toEqual({
    default_notice_days: 45,
    rules: [
      { country: "*", min_notice_days: 14, note: "Default floor of the code" },
      { country: "DE", min_notice_days: 60, note: "read by nobody" },
    ],
  });
  await expect(card).toContainText("operator@example.com");
  await expect(card.getByTestId("price-notice-saved")).toBeVisible();

  // A row removed is a row left out of the next save.
  await card.getByTestId("price-notice-remove-1").click();
  await card.getByTestId("price-notice-save").click();
  await expect.poll(() => gw.called("PUT", "/admin/v1/price-change-notice")).toHaveLength(2);
  expect((gw.called("PUT", "/admin/v1/price-change-notice")[1]?.body as { rules: unknown[] }).rules).toHaveLength(1);
});
