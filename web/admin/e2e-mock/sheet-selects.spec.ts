import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { LIVE, productGateway, signInAsAda } from "./product";

// Plan 03, complaint 9: the searchable lists (Combobox: currency, time zone, country, customer, package, an event list...) inside every sheet that has one.
// Sheets whose forms have no searchable list (a package, a coupon, a webhook endpoint, a meter: native selects and suggestions only) are not here. One data-driven test per sheet: the page behind is made tall and scrolled, the sheet is opened from its own button, and each list in it is opened with the pointer,
// walked with the keyboard and picked from; the page behind never moves, the page's scroll is locked once (by the stack of sheets, not again by the list), a list
// closes on Escape without closing the sheet, and when the sheet closes the page is where it was.

test.use({ viewport: { width: 1280, height: 720 }, actionTimeout: 10_000 });

interface SheetCase {
  name: string;
  /** The page, under the operator's tenant, or (`person`) a path of the person's console. */
  path: string;
  person?: boolean;
  mock: (gw: MockGateway) => MockGateway;
  /** Opens the sheet from its own button. */
  open: (page: Page) => Promise<void>;
  /** The lists the sheet has (so a list that disappears from a sheet is noticed). */
  lists: number;
}

const customer = (id: string) => ({ id: `id-${id}`, external_id: id, name: `Name ${id}`, email: `${id}@example.com`, billing_currency: "TRY", create_time: "2026-10-05T10:00:00Z" });
const customers = (gw: MockGateway) => gw.on((c) => (c.method === "GET" && c.path === "/v1/customers" ? { body: { customers: [customer("cus_1"), customer("cus_2"), customer("cus_3")], next_page_token: "" } } : undefined));
const plans = (gw: MockGateway) =>
  gw.on((c) => {
    if (c.method !== "GET") return undefined;
    if (c.path === "/v1/plans") return { body: { plans: [{ id: "p1", code: "basic", name: "Basic", latest_version: 1 }, { id: "p2", code: "pro", name: "Pro", latest_version: 1 }] } };
    return /^\/v1\/plans\/\w+\/versions\/latest$/.test(c.path) ? { body: { version: 1, base_fee_minor: "1000", fees: [], prices: [], publish_time: "2026-10-05T10:00:00Z" } } : undefined;
  });
// The button is clicked without scrolling to it (a real click first scrolls the page up to the button, which is not what a person with a long page does with a sheet's own button in reach).
const button = (name: string | RegExp) => async (page: Page) => void (await page.getByRole("button", { name }).first().dispatchEvent("click"));
const testId = (id: string) => async (page: Page) => void (await page.getByTestId(id).dispatchEvent("click"));

const CASES: SheetCase[] = [
  { name: "new customer (currency, country)", path: "/customers", mock: customers, open: testId("customers-new"), lists: 2 },
  { name: "new subscription (customer, package)", path: "/subscriptions", mock: (gw) => plans(customers(gw)).on((c) => (c.method === "GET" && c.path === "/v1/subscriptions" ? { body: { subscriptions: [] } } : undefined)), open: testId("subscriptions-new"), lists: 2 },
  { name: "new payment link (currency, customer)", path: "/payment-links", mock: customers, open: button("Create link"), lists: 2 },
  { name: "new credit pack (currency)", path: "/credits", mock: (gw) => customers(gw).on((c) => (c.method === "GET" && c.path === "/v1/credit-packs" ? { body: { packs: [] } } : undefined)), open: button("New credit pack"), lists: 1 },
  { name: "new expense (currency)", path: "/accounting", mock: (gw) => gw.on((c) => (c.path === "/v1/expenses" ? { body: { expenses: [] } } : c.path === "/v1/accounting/summary" ? { body: { currencies: [], from_date: "2026-10-01", to_date: "2026-10-31", granularity: "day" } } : undefined)), open: testId("add-expense"), lists: 1 },
  { name: "new app of the person (currency, time zone)", person: true, path: "/app?app=all", mock: (gw) => gw, open: button("Create app"), lists: 2 },
];

/** The page scrolls: a tall block at the end, and the page is 400 px down. */
async function scrollPage(page: Page): Promise<void> {
  await page.evaluate(() => {
    const spacer = document.createElement("div");
    spacer.id = "test-spacer";
    spacer.style.height = "3000px";
    document.body.append(spacer);
    window.scrollTo(0, 400);
  });
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(400);
}
/** What owns the page's scroll lock: the root element once, never the body as well (the list of a select or a menu may lock the body again). */
const locks = (page: Page) =>
  page.evaluate(() => ({
    root: document.documentElement.style.overflow,
    body: document.body.style.overflow,
    bodyAttr: document.body.hasAttribute("data-scroll-locked"),
    y: window.scrollY,
  }));

for (const c of CASES) {
  test(`${c.name}: every list in the sheet opens, takes the keyboard and a pick, and the page behind never moves or locks twice`, async ({ page }) => {
    const gw = c.mock(c.person ? productGateway(new MockGateway()).gw : baseTenant(new MockGateway()));
    await gw.install(page);
    if (c.person) {
      await signInAsAda(page);
      await page.goto(c.path);
    } else {
      await openTenantPage(page, c.path);
    }
    await scrollPage(page);
    await c.open(page);
    const sheet = page.getByRole("dialog").first();
    await expect(sheet).toBeVisible();

    // One lock, owned by the stack of sheets, with the page still where it was.
    await expect.poll(() => locks(page)).toMatchObject({ root: "hidden", body: "", bodyAttr: false, y: 400 });

    const lists = sheet.locator('input[role="combobox"]');
    await expect(lists).toHaveCount(c.lists);
    for (let i = 0; i < c.lists; i++) {
      const field = lists.nth(i);
      await field.scrollIntoViewIfNeeded();
      await field.click();
      // The list is the field's sibling: field, its frame, and the root that holds the list.
      const listbox = field.locator("xpath=../..").getByRole("listbox");
      await expect(field).toHaveAttribute("aria-expanded", "true");
      await expect(listbox.getByRole("option").first()).toBeVisible();
      // The list is wholly inside the window and the page did not move to show it.
      const box = await listbox.boundingBox();
      expect(box?.x ?? -1).toBeGreaterThanOrEqual(0);
      expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(1280);
      expect(await locks(page)).toMatchObject({ root: "hidden", body: "", bodyAttr: false, y: 400 });

      // The keyboard: the arrow names an option, and Enter takes it (a list of several stays open for more).
      const many = (await listbox.getAttribute("aria-multiselectable")) === "true";
      await page.keyboard.press("ArrowDown");
      await expect(field).toHaveAttribute("aria-activedescendant", /-opt-\d+$/);
      await page.keyboard.press("Enter");
      if (many) {
        await expect(field).toHaveAttribute("aria-expanded", "true");
        await expect(sheet.getByTestId("combobox-chip").first()).toBeVisible();
        // Escape closes the list and leaves the sheet.
        await page.keyboard.press("Escape");
      }
      await expect(field).toHaveAttribute("aria-expanded", "false");
      await expect(sheet).toBeVisible();
      expect(await locks(page)).toMatchObject({ root: "hidden", body: "", bodyAttr: false, y: 400 });
    }

    // Closing the sheet gives the page back where it was.
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect.poll(() => locks(page)).toMatchObject({ root: "", body: "", bodyAttr: false, y: 400 });
  });
}

// --- a sheet that holds a list and was reached by the address (a reload or a shared link): the same lists work there too

test("a sheet named in the address opens with its lists working, and Escape closes the list before the sheet", async ({ page }) => {
  const gw = customers(baseTenant(new MockGateway()));
  await gw.install(page);
  await openTenantPage(page, "/customers?sheet=new-customer");
  const sheet = page.getByRole("dialog").first();
  await expect(sheet).toBeVisible();
  const currency = sheet.locator('input[role="combobox"]').first();
  await currency.click();
  await expect(currency).toHaveAttribute("aria-expanded", "true");
  await page.keyboard.press("Escape");
  await expect(currency).toHaveAttribute("aria-expanded", "false");
  await expect(sheet).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect.poll(() => new URL(page.url()).searchParams.get("sheet")).toBeNull();
});

// The sheets that hold a list on the person's side use the same component; one of them stands for them (the tenant of the person's app).
test("the person's own new-payment-link sheet works the same", async ({ page }) => {
  const { gw } = productGateway(new MockGateway());
  customers(gw);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/payment-links`);
  await scrollPage(page);
  await page.getByRole("button", { name: "Create link" }).first().dispatchEvent("click");
  const sheet = page.getByRole("dialog").first();
  await expect(sheet).toBeVisible();
  const field = sheet.locator('input[role="combobox"]').first();
  await field.click();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(field).toHaveAttribute("aria-expanded", "false");
  expect(await locks(page)).toMatchObject({ root: "hidden", body: "", bodyAttr: false, y: 400 });
});
