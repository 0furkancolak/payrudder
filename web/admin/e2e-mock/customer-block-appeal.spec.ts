import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { confirmDialog } from "./ui";

// Plan 13: the appeal of a blocked customer, on the seller's side: the customer's sheet shows the statement as text, and the seller rejects (an answer is required) or
// accepts (the block is lifted). What a viewer sees of it is in viewer-gating.spec.ts.

test.use({ viewport: { width: 1440, height: 900 } });

const STATEMENT = 'It was a mistake <img src=x onerror="window.__pwned=1"> second line';
const base = { id: "id-1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", create_time: "2026-10-01T10:00:00Z" };

test("the seller reads the statement as text, and rejects with an answer, which keeps the block", async ({ page }) => {
  let blocked = true;
  let appeals: Record<string, unknown>[] = [{ id: "ap1", status: "open", statement: STATEMENT, answer: "", decided_by: "", create_time: "2026-11-01T10:00:00Z", decide_time: null }];
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [blocked ? { ...base, block_time: "2026-10-28T10:00:00Z", block_category: "fraud", block_reason: "stolen card" } : base] } };
    if (c.method === "GET" && c.path === "/v1/customers/cus_1/appeals") return { body: { appeals } };
    const m = /^\/v1\/customers\/cus_1\/appeals\/(ap\d)\/decision$/.exec(c.path);
    if (c.method === "POST" && m) {
      const decided = { ...appeals[0], status: c.body?.decision, answer: c.body?.answer ?? "", decided_by: "ada@example.com", decide_time: "2026-11-02T10:00:00Z" };
      appeals = [decided];
      if (c.body?.decision === "accepted") blocked = false;
      return { body: decided };
    }
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_1").click();

  // The customer's words are text: the markup is shown, nothing ran, and the box says it is untrusted.
  const panel = page.getByTestId("customer-appeal");
  await expect(page.getByTestId("customer-appeal-statement")).toContainText('<img src=x onerror="window.__pwned=1">');
  await expect(page.getByTestId("customer-appeal-statement").locator("img")).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned)).toBeUndefined();
  await expect(panel).toContainText("untrusted");
  await expect(page.getByTestId("customer-appeal-status")).toHaveText("Waiting for you");

  // Rejecting needs an answer; the block stays.
  await expect(page.getByTestId("customer-appeal-reject")).toBeDisabled();
  await page.getByTestId("customer-appeal-answer-input").fill("We checked: the card was stolen.");
  await page.getByTestId("customer-appeal-reject").click();
  await expect(page.getByTestId("customer-appeal-status")).toHaveText("Rejected");
  await expect(page.getByTestId("customer-appeal-answer")).toContainText("We checked: the card was stolen.");
  expect(gw.called("POST", "/v1/customers/cus_1/appeals/ap1/decision")[0]?.body).toEqual({ decision: "rejected", answer: "We checked: the card was stolen." });
  await expect(page.getByTestId("customer-block-banner")).toBeVisible();
  await expect(page.getByTestId("customer-appeal-reject")).toHaveCount(0); // a decided appeal has no buttons
});

test("accepting asks first, lifts the block, and sends the decision without an answer", async ({ page }) => {
  let blocked = true;
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [blocked ? { ...base, block_time: "2026-10-28T10:00:00Z" } : base] } };
    if (c.method === "GET" && c.path === "/v1/customers/cus_1/appeals") return { body: { appeals: [{ id: "ap2", status: blocked ? "open" : "accepted", statement: "Second try", answer: "", decided_by: "", create_time: "2026-11-02T12:00:00Z", decide_time: null }] } };
    if (c.method === "POST" && c.path === "/v1/customers/cus_1/appeals/ap2/decision") {
      blocked = false;
      return { body: { id: "ap2", status: "accepted" } };
    }
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_1").click();
  await expect(page.getByTestId("customer-appeal-statement")).toContainText("Second try");
  await page.getByTestId("customer-appeal-accept").click();
  await confirmDialog(page, "no");
  expect(gw.called("POST", "/v1/customers/cus_1/appeals/ap2/decision")).toHaveLength(0); // declined: nothing was sent
  await page.getByTestId("customer-appeal-accept").click();
  await confirmDialog(page);
  await expect(page.getByTestId("customer-block-banner")).toHaveCount(0);
  expect(gw.called("POST", "/v1/customers/cus_1/appeals/ap2/decision")[0]?.body).toEqual({ decision: "accepted" });
});

test("a customer who is not blocked shows no appeal, and a blocked one with none shows no box", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [{ ...base, block_time: "2026-10-28T10:00:00Z" }, { ...base, id: "id-2", external_id: "cus_2", name: "Grace" }] } };
    if (c.method === "GET" && c.path.endsWith("/appeals")) return { body: { appeals: [] } };
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await page.getByTestId("customer-cus_1").click();
  await expect(page.getByTestId("customer-block-banner")).toBeVisible();
  await expect(page.getByTestId("customer-appeal")).toHaveCount(0);
  expect(gw.called("GET", "/v1/customers/cus_1/appeals")).toHaveLength(1);
});
