import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, type Call } from "./gateway";
import { ACCOUNT, LIVE, productGateway, signInAsAda } from "./product";
import { pickLanguage } from "./ui";

// The rejected-event viewer on the Connect page (plan 18): the events the API refused with the hint for each code, the 24-hour summary whose codes are filters, the
// meter filter, "show more" through the cursor, and the empty, failed and viewer cases.

test.use({ viewport: { width: 1440, height: 900 } });

const row = (n: number, over: Record<string, unknown> = {}) => ({
  id: `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`,
  error_code: "UNKNOWN_METER",
  error_message: `meter "gb_${n}" does not exist`,
  meter_code: `gb_${n}`,
  customer_external_id: "cus_1",
  event_time: "2026-11-03T10:15:00Z",
  received_time: "2026-11-03T10:15:02Z",
  idempotency_key: `k-${n}`,
  ...over,
});
const SUMMARY = {
  total: 3,
  by_code: [
    { error_code: "UNKNOWN_METER", count: 2 },
    { error_code: "INVALID_QUANTITY", count: 1 },
  ],
  hourly: Array.from({ length: 24 }, (_, i) => ({ hour: `2026-11-03T${String(i).padStart(2, "0")}:00:00Z`, rejected: i === 23 ? 3 : 0 })),
};
const ROWS = [row(1), row(2), row(3, { error_code: "INVALID_QUANTITY", meter_code: "api_calls", event_time: null })];

/** The log: filters by code and meter as the API does, 2 rows a page with a cursor. */
function log(rows: unknown[] = ROWS, summary: unknown = SUMMARY) {
  return (c: Call) => {
    if (c.method !== "GET") return undefined;
    if (c.path === "/v1/meters") return { body: { meters: [] } };
    if (c.path === "/v1/customers") return { body: { customers: [] } };
    if (c.path === "/v1/webhook-endpoints") return { body: { endpoints: [] } };
    if (c.path === "/v1/rejected-events/summary") return { body: summary };
    if (c.path === "/v1/rejected-events") {
      const code = c.query.get("error_code") ?? "";
      const meter = c.query.get("meter") ?? "";
      const all = (rows as ReturnType<typeof row>[]).filter((r) => (!code || r.error_code === code) && (!meter || r.meter_code === meter));
      const from = Number(c.query.get("page_token") ?? 0);
      const page = all.slice(from, from + 2);
      return { body: { events: page, next_page_token: from + 2 < all.length ? String(from + 2) : "" } };
    }
    return undefined;
  };
}
const gateway = (rows?: unknown[], summary?: unknown) => baseTenant(new MockGateway()).on(log(rows, summary));

test("rejected events are listed with the hint for their code, and a page more is loaded through the cursor", async ({ page }) => {
  const gw = gateway();
  await gw.install(page);
  await openTenantPage(page, "/connect");
  const section = page.getByTestId("rejected-events");
  await expect(section.getByTestId("rejected-total")).toContainText("3 rejected in the last 24 hours");
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000001")).toBeVisible();
  const first = section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000001");
  await expect(first).toContainText("UNKNOWN_METER");
  await expect(first.getByTestId("rejected-hint")).toContainText("No meter has this code");
  await expect(first).toContainText("gb_1");
  await expect(first).toContainText("cus_1");
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000003")).toHaveCount(0);

  await section.getByTestId("rejected-more").click();
  const third = section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000003");
  await expect(third).toContainText("INVALID_QUANTITY");
  await expect(third).toContainText("—"); // no event time
  await expect(section.getByTestId("rejected-more")).toHaveCount(0);
  expect(gw.called("GET", "/v1/rejected-events").map((c) => c.query.get("page_token"))).toEqual([null, "2"]);
});

test("a code of the summary and the meter box filter the list on the server, and clearing brings the rest back", async ({ page }) => {
  const gw = gateway();
  await gw.install(page);
  await openTenantPage(page, "/connect");
  const section = page.getByTestId("rejected-events");
  await expect(section.getByTestId("rejected-chip-INVALID_QUANTITY")).toBeVisible();

  await section.getByTestId("rejected-chip-INVALID_QUANTITY").click();
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000003")).toBeVisible();
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000001")).toHaveCount(0);
  await expect(section.getByTestId("rejected-code")).toHaveValue("INVALID_QUANTITY");

  await section.getByTestId("rejected-clear").click();
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000001")).toBeVisible();

  await section.getByTestId("rejected-meter").fill("gb_2");
  await section.getByTestId("rejected-meter").press("Enter");
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000002")).toBeVisible();
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000001")).toHaveCount(0);
  expect(gw.called("GET", "/v1/rejected-events").some((c) => c.query.get("meter") === "gb_2")).toBe(true);

  // A filter with no match says so, rather than "nothing was rejected".
  await section.getByTestId("rejected-meter").fill("nope");
  await section.getByTestId("rejected-meter").press("Enter");
  await expect(section.getByTestId("rejected-empty")).toContainText("Nothing matches");
});

test("with nothing rejected the section says so and shows no summary", async ({ page }) => {
  const gw = gateway([], { total: 0, by_code: [], hourly: [] });
  await gw.install(page);
  await openTenantPage(page, "/connect");
  const section = page.getByTestId("rejected-events");
  await expect(section.getByTestId("rejected-empty")).toContainText("No event has been rejected");
  await expect(section.getByTestId("rejected-summary")).toHaveCount(0);
});

test("a log that did not load says so with a way to retry, and shows its rows once the retry works", async ({ page }) => {
  let failing = true;
  // The last handler added answers first.
  const gw = baseTenant(new MockGateway())
    .on(log())
    .on((c) =>
      failing && c.method === "GET" && c.path === "/v1/rejected-events"
        ? { status: 503, body: { error: { code: "UNAVAILABLE", key: "gateway.unavailable", message: "try later", request_id: "req-9" } } }
        : undefined,
    );
  await gw.install(page);
  await openTenantPage(page, "/connect");
  const section = page.getByTestId("rejected-events");
  await expect(section.getByTestId("error-state")).toContainText("req-9", { timeout: 30_000 });
  await expect(section.getByTestId("rejected-empty")).toHaveCount(0);
  failing = false;
  await section.getByTestId("error-retry").click();
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000001")).toBeVisible();
});

test("a viewer reads the rejected events too", async ({ page }) => {
  const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role: "viewer" }] });
  gw.on((c) => {
    if (c.method !== "GET") return undefined;
    if (c.path === "/v1/meters") return { body: { meters: [] } };
    if (c.path === "/v1/customers") return { body: { customers: [] } };
    if (c.path === "/v1/webhook-endpoints") return { body: { endpoints: [] } };
    if (c.path === "/v1/rejected-events/summary") return { body: SUMMARY };
    if (c.path === "/v1/rejected-events") return { body: { events: ROWS.slice(0, 2), next_page_token: "" } };
    return undefined;
  });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/connect`);
  await expect(page.getByTestId("rejected-events").getByTestId("rejected-row-00000000-0000-4000-8000-000000000001")).toBeVisible();
  await expect(page.getByTestId("connect-key-create")).toHaveCount(0); // and is offered nothing that writes
});

test("the section reads right to left", async ({ page }) => {
  const gw = gateway();
  await gw.install(page);
  await openTenantPage(page, "/connect");
  await pickLanguage(page, "ar");
  await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
  const section = page.getByTestId("rejected-events");
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000001")).toBeVisible();
  // Codes, meters and customers keep their own (left-to-right) direction inside the right-to-left page.
  await expect(section.getByTestId("rejected-row-00000000-0000-4000-8000-000000000001").locator("[dir=ltr]").first()).toContainText("UNKNOWN_METER");
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(overflow).toBe(false);
});
