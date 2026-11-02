import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { confirmDialog } from "./ui";

test.describe("mail", () => {
  test("a category is switched off with a partial update and shown off", async ({ page }) => {
    const off = new Set<string>();
    const all = ["invoice_issued", "payment_receipts", "payment_failed", "usage_alerts", "system"];
    const state = () => ({ categories: all.map((category) => ({ category, enabled: !off.has(category) })) });
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path !== "/v1/mail-settings") return undefined;
      if (c.method === "PUT") {
        for (const s of (c.body?.categories ?? []) as { category: string; enabled: boolean }[]) {
          if (s.enabled) off.delete(s.category);
          else off.add(s.category);
        }
      }
      return { body: state() };
    });
    await gw.install(page);
    await openTenantPage(page, "/mail");
    await expect(page.getByTestId("mail-payment_receipts")).toBeChecked();
    await page.getByTestId("mail-payment_receipts").click();
    await expect.poll(() => gw.called("PUT", "/v1/mail-settings").length).toBe(1);
    expect(gw.called("PUT", "/v1/mail-settings")[0]?.body).toEqual({ categories: [{ category: "payment_receipts", enabled: false }] });
    await expect(page.getByTestId("mail-payment_receipts")).not.toBeChecked();
    await expect(page.getByTestId("mail-usage_alerts")).toBeChecked();
  });

  test("the suppression list shows why each address is held back and mails one again after asking", async ({ page }) => {
    let rows = [
      { address: "ada@example.com", reason: "bounce", detail: "550 no such user", create_time: "2026-11-01T09:00:00Z" },
      { address: "bob@example.com", reason: "complaint", detail: "", create_time: "2026-11-02T09:00:00Z" },
      { address: "cyd@example.com", reason: "manual", detail: "asked not to be mailed", create_time: "2026-11-02T10:00:00Z" },
    ];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/mail-settings") return { body: { categories: [] } };
      if (c.path === "/v1/mail-messages") return { body: { messages: [], next_page_token: "", connected: true } };
      if (c.method === "GET" && c.path === "/v1/mail-suppressions") {
        // Two to a page, to see the token followed.
        const after = c.query.get("page_token");
        const start = after ? rows.findIndex((r) => r.address === after) + 1 : 0;
        const page = rows.slice(start, start + 2);
        return { body: { suppressions: page, next_page_token: start + 2 < rows.length ? (page.at(-1)?.address ?? "") : "", connected: true } };
      }
      if (c.method === "DELETE" && c.path.startsWith("/v1/mail-suppressions/")) {
        const address = decodeURIComponent(c.path.split("/").pop() ?? "");
        if (!rows.some((r) => r.address === address)) return { status: 404, body: { error: { code: "NOT_FOUND", key: "gateway.mail.suppression_not_found", message: "no" } } };
        rows = rows.filter((r) => r.address !== address);
        return { status: 204, body: {} };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/mail");
    const card = page.getByTestId("mail-suppressions");
    await expect(card.getByTestId("suppression-ada@example.com")).toContainText("Bounced for good");
    await expect(card.getByTestId("suppression-ada@example.com")).toContainText("550 no such user");
    await expect(card.getByTestId("suppression-bob@example.com")).toContainText("Complaint");
    await expect(card.getByTestId("suppression-cyd@example.com")).toHaveCount(0);
    await card.getByTestId("suppressions-more").click();
    await expect(card.getByTestId("suppression-cyd@example.com")).toContainText("Added by the operator");
    await expect(card.getByTestId("suppressions-more")).toHaveCount(0);
    expect(gw.called("GET", "/v1/mail-suppressions").map((c) => c.query.get("page_token"))).toEqual([null, "bob@example.com"]);

    // Mailing an address again asks first, names it, and sends nothing when the answer is no.
    await card.getByTestId("suppression-remove-ada@example.com").click();
    await expect(page.getByTestId("confirm-dialog")).toContainText("ada@example.com");
    await confirmDialog(page, "no");
    expect(gw.called("DELETE", /\/v1\/mail-suppressions\//)).toHaveLength(0);
    await card.getByTestId("suppression-remove-ada@example.com").click();
    await confirmDialog(page);
    await expect(card.getByTestId("suppression-ada@example.com")).toHaveCount(0);
    expect(gw.called("DELETE", /\/v1\/mail-suppressions\//)[0]?.path).toBe("/v1/mail-suppressions/ada%40example.com");
  });

  test("the message log lists recent mail with what became of each, a page at a time, and says when the notifier is not connected", async ({ page }) => {
    const m = (key: string, state: string, to: string) => ({ key, kind: "command", template: "invoice_paid.v1", recipient: to, state, claim_time: "2026-11-02T09:00:00Z", send_time: state === "sending" ? null : "2026-11-02T09:00:01Z" });
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/mail-settings") return { body: { categories: [] } };
      if (c.path === "/v1/mail-suppressions") return { body: { suppressions: [], next_page_token: "", connected: false } };
      if (c.method === "GET" && c.path === "/v1/mail-messages") {
        return c.query.get("page_token") === "next"
          ? { body: { messages: [m("k3", "bounced", "cyd@example.com")], next_page_token: "", connected: true } }
          : { body: { messages: [m("k1", "delivered", "ada@example.com"), m("k2", "sending", "bob@example.com")], next_page_token: "next", connected: true } };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/mail");
    const log = page.getByTestId("mail-messages");
    await expect(log.getByTestId("message-k1")).toContainText("Delivered");
    await expect(log.getByTestId("message-k2")).toContainText("Sending");
    await expect(log.getByTestId("message-k1")).toContainText("ada@example.com");
    await expect(log.getByTestId("message-k3")).toHaveCount(0);
    await log.getByTestId("messages-more").click();
    await expect(log.getByTestId("message-k3")).toContainText("Bounced");
    // The suppression list says the notifier is not connected (a 200 with connected=false, not an error), in the reader's language, and the log is not affected.
    await expect(page.getByTestId("mail-suppressions-not-connected")).toContainText("not connected on this deployment");
    await expect(page.getByTestId("mail-suppressions-empty")).toHaveCount(0);
  });

  test("a deployment without the notifier opens the mail page quietly: both lists say they are not connected and no mail request is refused", async ({ page }) => {
    // The browser logs a "Failed to load resource" error for every response of 400 or more; the mock refuses other calls of the page, so only the mail routes are watched.
    const refused: string[] = [];
    page.on("response", (r) => {
      if (r.url().includes("/v1/mail-") && r.status() >= 400) refused.push(`${r.status()} ${r.url()}`);
    });
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/mail-settings") return { body: { categories: [] } };
      if (c.method === "GET" && c.path === "/v1/mail-suppressions") return { body: { suppressions: [], next_page_token: "", connected: false } };
      if (c.method === "GET" && c.path === "/v1/mail-messages") return { body: { messages: [], next_page_token: "", connected: false } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/mail");
    await expect(page.getByTestId("mail-suppressions-not-connected")).toBeVisible();
    await expect(page.getByTestId("mail-messages-not-connected")).toBeVisible();
    expect(refused).toEqual([]);
    expect(gw.called("GET", "/v1/mail-suppressions").length).toBeGreaterThan(0);
    expect(gw.called("GET", "/v1/mail-messages").length).toBeGreaterThan(0);
  });
});
