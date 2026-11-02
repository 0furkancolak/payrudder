import { expect, test, type Page } from "@playwright/test";
import { MockGateway, signInAsOperator } from "./gateway";
import { LIVE, productGateway, signInAsAda } from "./product";
import { confirmDialog, pickFilter } from "./ui";

// The blocklists (plan 13): the app's own page for a person (the owner adds and removes, a viewer only reads), and the operator's platform-wide page. The gateway is
// mocked; it normalises a value the way the server does and keeps the list.

type Entry = { id: string; kind: string; value: string; note: string; created_by: string; create_time: string };
const entry = (n: number, kind: string, value: string, note = ""): Entry => ({ id: `0192f0b4-0000-7c3a-9a3e-00000000000${n}`, kind, value, note, created_by: "ada@example.com", create_time: `2026-11-0${n}T09:00:00Z` });

/** A list that answers the tenant API (/v1/blocklist) or the operator's (/admin/v1/blocklist) and keeps what it was sent. */
function list(gw: MockGateway, base: string, entries: Entry[]) {
  gw.on((c) => {
    if (c.path === base && c.method === "GET") return { body: { entries } };
    if (c.path === base && c.method === "POST") {
      const b = c.body as { kind: string; value: string; note?: string };
      const e = entry(entries.length + 1, b.kind, b.kind === "email" ? b.value.toLowerCase() : b.value, b.note ?? "");
      entries.unshift(e);
      return { status: 201, body: e };
    }
    if (c.path.startsWith(`${base}/`) && c.method === "DELETE") {
      const i = entries.findIndex((e) => e.id === c.path.split("/").pop());
      if (i < 0) return { status: 404, body: { error: { code: "NOT_FOUND", key: "plans.common.not_found", message: "no" } } };
      entries.splice(i, 1);
      return { status: 204, body: {} };
    }
    return undefined;
  });
}

const row = (page: Page, value: string) => page.getByTestId(new RegExp(`^blocklist-.*-${value.replace(/[.@+]/g, "\\$&")}$`));

test.describe("an app's blocklist", () => {
  test("is read, narrowed by kind on its button, added to and removed from (after a question)", async ({ page }) => {
    const entries = [entry(1, "email", "fraud@example.com", "stole a card"), entry(2, "tax_id", "TR1234567890"), entry(3, "external_id", "cus_gone")];
    const gw = productGateway(new MockGateway()).gw;
    list(gw, "/v1/blocklist", entries);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/blocklist`);
    await expect(page.getByTestId("sidebar").getByRole("link", { name: "Blocklist" })).toBeVisible();
    await expect(row(page, "fraud@example.com")).toContainText("stole a card");

    // The kind filter applies on its button, like every filter.
    await pickFilter(page, "Kind", "Tax id");
    await expect(row(page, "cus_gone")).toHaveCount(0);
    await expect(row(page, "TR1234567890")).toBeVisible();
    await page.getByRole("button", { name: "Clear filters" }).first().click();
    await expect(row(page, "cus_gone")).toBeVisible();

    // Adding: the value is sent as typed, the server normalises it, the list shows what came back.
    await page.getByTestId("blocklist-new").click();
    await page.getByTestId("blocklist-value").fill("New@Example.com");
    await page.getByTestId("blocklist-note").fill("chargeback twice");
    await page.getByTestId("blocklist-submit").click();
    await expect(row(page, "new@example.com")).toContainText("chargeback twice");
    expect(gw.called("POST", "/v1/blocklist")[0]?.body).toEqual({ kind: "email", value: "New@Example.com", note: "chargeback twice" });

    // Removing asks first; "no" changes nothing, "yes" sends the one request.
    const fraud = row(page, "fraud@example.com");
    await fraud.getByTestId("row-action-remove").click();
    await confirmDialog(page, "no");
    await expect(fraud).toBeVisible();
    expect(gw.called("DELETE", "/v1/blocklist/0192f0b4-0000-7c3a-9a3e-000000000001")).toHaveLength(0);
    await fraud.getByTestId("row-action-remove").click();
    await confirmDialog(page, "yes");
    await expect(fraud).toHaveCount(0);
    expect(gw.called("DELETE", "/v1/blocklist/0192f0b4-0000-7c3a-9a3e-000000000001")).toHaveLength(1);
  });

  test("a viewer reads the list and is offered no control to change it", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", account_name: "Ada Labs", role: "viewer" }] });
    list(gw, "/v1/blocklist", [entry(1, "email", "fraud@example.com")]);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/blocklist`);
    await expect(row(page, "fraud@example.com")).toBeVisible();
    await expect(page.getByTestId("blocklist-new")).toHaveCount(0);
    await expect(page.getByTestId("row-action-remove")).toHaveCount(0);
  });

  test("an empty list says what it is for", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    list(gw, "/v1/blocklist", []);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/blocklist`);
    await expect(page.getByTestId("blocklist-empty")).toContainText("Nothing on the blocklist");
  });
});

test.describe("the platform blocklist", () => {
  test("is the operator's page: read, added to and removed from through the admin API", async ({ page }) => {
    const entries = [entry(1, "email", "ring@example.com", "fraud ring")];
    const gw = new MockGateway();
    list(gw, "/admin/v1/blocklist", entries);
    gw.on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [] } } : undefined));
    await gw.install(page);
    await signInAsOperator(page);
    await page.getByTestId("sidebar").getByRole("link", { name: "Platform blocklist" }).click();
    await expect(row(page, "ring@example.com")).toContainText("fraud ring");
    await page.getByTestId("blocklist-new").click();
    await page.getByTestId("blocklist-kind").selectOption("tax_id");
    await page.getByTestId("blocklist-value").fill("TR99");
    await page.getByTestId("blocklist-submit").click();
    await expect(row(page, "TR99")).toBeVisible();
    expect(gw.called("POST", "/admin/v1/blocklist")[0]?.body).toEqual({ kind: "tax_id", value: "TR99" });
  });
});
