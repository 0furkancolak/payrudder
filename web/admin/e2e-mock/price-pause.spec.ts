import { expect, test } from "@playwright/test";
import { MockGateway, signInAsOperator } from "./gateway";
import { confirmDialog } from "./ui";

// The operator's kill switch for price changes (plan 15): off at first, pulled with a reason after asking, shown with who and why, and released after asking.

test.use({ viewport: { width: 1440, height: 900 } });

test("the operator pauses every price change with a reason, sees who and why, and resumes", async ({ page }) => {
  const gw = new MockGateway();
  gw.on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [] } } : undefined));
  await gw.install(page);
  await signInAsOperator(page);
  const card = page.getByTestId("price-pause");
  await expect(card.getByTestId("price-pause-state")).toHaveText("Running");
  await expect(card.getByTestId("price-pause-by")).toHaveCount(0);

  await card.getByTestId("price-pause-reason").fill("wrong target version");
  await card.getByTestId("price-pause-toggle").click();
  await confirmDialog(page, "no");
  expect(gw.called("PUT", "/admin/v1/price-change-pause")).toHaveLength(0);
  await card.getByTestId("price-pause-toggle").click();
  await confirmDialog(page);
  await expect(card.getByTestId("price-pause-state")).toHaveText("Paused");
  expect(gw.called("PUT", "/admin/v1/price-change-pause")[0]?.body).toEqual({ paused: true, reason: "wrong target version" });
  await expect(card.getByTestId("price-pause-by")).toContainText("operator@example.com");
  await expect(card.getByTestId("price-pause-by")).toContainText("wrong target version");
  await expect(card.getByTestId("price-pause-reason")).toHaveCount(0);

  await card.getByTestId("price-pause-toggle").click();
  await confirmDialog(page);
  await expect(card.getByTestId("price-pause-state")).toHaveText("Running");
  expect(gw.called("PUT", "/admin/v1/price-change-pause")[1]?.body).toEqual({ paused: false, reason: "" });
});
