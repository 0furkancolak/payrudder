import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, signInAsOperator } from "./gateway";
import { pickFilters } from "./ui";

// The shared controls of phase 50: the combobox (one value, several, creatable, from an API), the multi-value filter chip kept in the address, the date
// range at a phone's width, and the states of a failed page. The combobox and the date range are tried on the operator's component page.

async function openComponents(page: Page) {
  const gw = baseTenant(new MockGateway());
  await gw.install(page);
  await signInAsOperator(page);
  await page.goto("/playground/components");
  await expect(page.getByRole("heading", { name: "Components" })).toBeVisible();
  return gw;
}

test.describe("combobox", () => {
  test("one value: shows the first ten, narrows by typing, says how many more, and is keyboard complete", async ({ page }) => {
    await openComponents(page);
    const field = page.getByLabel("One choice");
    await field.click();
    const list = page.getByRole("listbox");
    await expect(list.getByRole("option")).toHaveCount(10);
    await expect(list.getByTestId("combobox-more")).toContainText("15 more");
    await expect(field).toHaveAttribute("aria-expanded", "true");
    // Typing narrows the whole list, not just the ten shown.
    await field.fill("berry");
    await expect(list.getByRole("option")).toHaveText(["Blackberry", "Blueberry", "Cranberry", "Elderberry", "Raspberry"]);
    // Arrow keys move, Enter picks, and the field then shows the choice.
    await field.press("ArrowDown");
    await expect(field).toHaveAttribute("aria-activedescendant", /-opt-1$/);
    await field.press("Enter");
    await expect(field).toHaveValue("Blueberry");
    await expect(page.getByTestId("cb-single-value")).toHaveText("blueberry");
    await expect(list).toBeHidden();
    // Escape closes without choosing; opening again puts the choice under the cursor.
    await field.press("ArrowDown");
    await expect(list).toBeVisible();
    await field.press("Escape");
    await expect(list).toBeHidden();
    await expect(field).toHaveValue("Blueberry");
  });

  test("several values: chips, a list that stays open, Backspace and the chip's cross remove", async ({ page }) => {
    await openComponents(page);
    const field = page.getByLabel("Several choices", { exact: true });
    await field.click();
    const list = page.getByRole("listbox");
    await expect(list).toHaveAttribute("aria-multiselectable", "true");
    await list.getByRole("option", { name: "Apple" }).click();
    await list.getByRole("option", { name: "Banana" }).click();
    await expect(list).toBeVisible();
    await expect(list.getByRole("option", { name: "Apple" })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByTestId("cb-multi-value")).toHaveText("apple,banana");
    const chips = page.getByTestId("combobox-chip");
    await expect(chips.filter({ hasText: "Apple" })).toHaveCount(1);
    await field.press("Backspace");
    await expect(page.getByTestId("cb-multi-value")).toHaveText("apple");
    await page.getByRole("button", { name: "Remove Apple" }).click();
    await expect(page.getByTestId("cb-multi-value")).toHaveText("");
  });

  test("creatable: the typed text is offered as a new value", async ({ page }) => {
    await openComponents(page);
    const field = page.getByLabel("Several, or a new one");
    await field.click();
    await field.fill("Dragonfruit");
    const list = page.getByRole("listbox");
    await expect(list.getByRole("option")).toHaveText(["Create “Dragonfruit”"]);
    await field.press("Enter");
    await expect(page.getByTestId("cb-tags-value")).toHaveText("Dragonfruit");
    await expect(page.getByTestId("combobox-chip")).toContainText("Dragonfruit");
    // A text that already is an option is not offered again.
    await field.fill("Fig");
    await expect(list.getByRole("option")).toHaveText(["Fig"]);
  });

  test("from an API: waits for a pause in typing, shows a loading state, drops stale answers", async ({ page }) => {
    await openComponents(page);
    const field = page.getByLabel("From an API", { exact: true });
    const calls = page.getByTestId("cb-remote-calls");
    await field.click();
    await expect(page.getByTestId("combobox-loading")).toBeVisible();
    await expect(page.getByTestId("combobox-skeleton")).toHaveCount(3);
    await expect(calls).toHaveText("1");
    const list = page.getByRole("listbox");
    await expect(list.getByRole("option")).toHaveCount(10);
    await expect(page.getByTestId("combobox-loading")).toBeHidden();
    // Typing fast asks once, after the pause.
    await field.pressSequentially("ap", { delay: 30 });
    await expect(page.getByTestId("combobox-loading")).toBeVisible();
    await expect(list.getByRole("option")).toHaveText(["Apple", "Apricot", "Grape", "Papaya"]);
    await expect(calls).toHaveText("2");
    // A new question while one is on its way: only the last answer is shown.
    await field.fill("lem");
    await field.fill("kiw");
    await expect(list.getByRole("option")).toHaveText(["Kiwi"]);
    await list.getByRole("option", { name: "Kiwi" }).click();
    await expect(field).toHaveValue("Kiwi");
    await expect(page.getByTestId("cb-remote-value")).toHaveText("kiwi");
  });

  test("from an API that fails: the failure is in the list, with a way to try again", async ({ page }) => {
    await openComponents(page);
    const field = page.getByLabel("From an API that fails once");
    await field.click();
    const failure = page.getByTestId("combobox-error");
    await expect(failure).toContainText("The service is not available right now");
    await failure.getByRole("button", { name: "Try again" }).click();
    const list = page.getByRole("listbox");
    await expect(list.getByRole("option")).toHaveCount(10);
    await expect(failure).toBeHidden();
  });

  test("the live region says how many results there are", async ({ page }) => {
    await openComponents(page);
    const field = page.getByLabel("One choice");
    await field.click();
    await expect(page.getByRole("status").filter({ hasText: "results" }).first()).toHaveText("10 results");
  });
});

test.describe("filters with several values", () => {
  const invoices = [
    { id: "i1", number: "INV-1", customer_external_id: "cus_1", status: "paid", currency: "TRY", total_minor: "100", create_time: "2026-10-01T10:00:00Z" },
    { id: "i2", number: "INV-2", customer_external_id: "cus_1", status: "open", currency: "TRY", total_minor: "200", create_time: "2026-10-02T10:00:00Z" },
    { id: "i3", number: "INV-3", customer_external_id: "cus_2", status: "void", currency: "TRY", total_minor: "300", create_time: "2026-10-03T10:00:00Z" },
    { id: "i4", number: "INV-4", customer_external_id: "cus_2", status: "paid", currency: "TRY", total_minor: "400", create_time: "2026-10-04T10:00:00Z" },
  ];
  const gateway = () =>
    baseTenant(new MockGateway()).on((c) => {
      if (c.method === "GET" && c.path === "/admin/v1/tenants/0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e/invoices") return { body: { invoices } };
      return undefined;
    });

  test("a row passes when it matches any chosen value; the choice is in the address and survives a reload", async ({ page }) => {
    await gateway().install(page);
    await openTenantPage(page, "/invoices");
    await expect(page.getByRole("row", { name: /INV-1/ })).toBeVisible();
    await expect(page.getByRole("row")).toHaveCount(5);

    await pickFilters(page, "Status", "paid", "open");
    await expect(page.getByRole("row", { name: /INV-3/ })).toHaveCount(0);
    await expect(page.getByRole("row")).toHaveCount(4); // the header and three invoices
    const chip = page.getByRole("button", { name: "Status", exact: true });
    await expect(chip).toContainText("+1");
    await expect.poll(() => new URL(page.url()).searchParams.get("status")).toBe("paid,open");

    await page.reload();
    await expect(page.getByRole("row", { name: /INV-1/ })).toBeVisible();
    await expect(page.getByRole("row", { name: /INV-3/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Status", exact: true })).toContainText("+1");

    // Taking one value off keeps the other; clearing the chip takes the parameter off the address.
    await page.getByRole("button", { name: "Status", exact: true }).click();
    await page.getByRole("listbox", { name: "Status" }).getByRole("option", { name: "open" }).click();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("row")).toHaveCount(3);
    await page.getByRole("button", { name: "Status: ×" }).click();
    await expect(page.getByRole("row")).toHaveCount(5);
    await expect.poll(() => new URL(page.url()).searchParams.has("status")).toBe(false);
  });

  test("an address that names the values opens the list already filtered", async ({ page }) => {
    await gateway().install(page);
    await openTenantPage(page, "/invoices?status=void,open");
    await expect(page.getByRole("row", { name: /INV-3/ })).toBeVisible();
    await expect(page.getByRole("row", { name: /INV-1/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Status", exact: true })).toContainText("+1");
  });
});

test.describe("date range on a phone", () => {
  test.use({ viewport: { width: 375, height: 700 }, hasTouch: true, isMobile: true });

  test("the panel stays inside the screen and the native date fields work", async ({ page }) => {
    await openComponents(page);
    await page.getByTestId("date-range").click();
    const panel = page.getByTestId("date-range-panel");
    await expect(panel).toBeVisible();
    const box = await panel.boundingBox();
    expect(box).not.toBeNull();
    expect(box?.x ?? -1).toBeGreaterThanOrEqual(0);
    expect((box?.x ?? 0) + (box?.width ?? 999)).toBeLessThanOrEqual(375);
    const scroll = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(scroll).toBeLessThanOrEqual(0);
    // The two dates are the browser's own date fields.
    const from = panel.getByLabel("From");
    await expect(from).toHaveAttribute("type", "date");
    await from.fill("2026-10-01");
    await panel.getByLabel("To").fill("2026-10-15");
    await panel.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByTestId("cb-range-value")).toHaveText("2026-10-01…2026-10-15");
    // The presets are still there.
    await page.getByTestId("date-range").click();
    await page.getByTestId("range-today").click();
    await expect(page.getByTestId("cb-range-value")).not.toHaveText("2026-10-01…2026-10-15");
  });

  test("the panel stays inside the screen from the right edge too", async ({ page }) => {
    await openComponents(page);
    const trigger = page.getByTestId("date-range");
    // Push the trigger to the right edge of the page: the panel must slide back rather than cross it.
    await trigger.evaluate((el) => (el.parentElement ? (el.parentElement.style.justifyContent = "flex-end") : undefined));
    await trigger.click();
    const box = await page.getByTestId("date-range-panel").boundingBox();
    expect((box?.x ?? -1) >= 0 && (box?.x ?? 0) + (box?.width ?? 999) <= 375).toBe(true);
  });
});

test.describe("failed pages", () => {
  test("an address no page answers to shows the not-found page, with the way back", async ({ page }) => {
    await openComponents(page);
    await page.goto("/no/such/page");
    await expect(page.getByTestId("not-found")).toContainText("Page not found");
    await page.getByTestId("not-found").getByRole("link", { name: "Back to the console" }).click();
    await page.waitForURL("**/app");
  });

  test("a list that cannot be loaded is a state with a title, a translated message, the request id and a retry", async ({ page }) => {
    let failing = true;
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "GET" && c.path === "/v1/customers") {
        if (failing) return { status: 403, body: { error: { code: "PERMISSION_DENIED", key: "common.permission_denied", message: "denied", request_id: "req-7f3a" } } };
        return { body: { customers: [{ id: "id-1", external_id: "cus_1", name: "Ada", email: "ada@example.com", create_time: "2026-09-01T10:00:00Z" }] } };
      }
      if (c.method === "GET" && c.path.startsWith("/v1/")) return { body: { invoices: [], payments: [], subscriptions: [], plans: [], entries: [], lots: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    const state = page.getByTestId("error-state");
    await expect(state).toBeVisible();
    await expect(state.getByTestId("error-title")).toHaveText("You do not have access to this");
    await expect(state.getByTestId("error-request-id")).toContainText("req-7f3a");
    failing = false;
    await state.getByTestId("error-retry").click();
    await expect(page.getByTestId("customer-cus_1")).toBeVisible();
    await expect(state).toHaveCount(0);
  });

  test("the failure state speaks the console's language", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "GET" && c.path === "/v1/customers") return { status: 404, body: { error: { code: "NOT_FOUND", key: "common.not_found", message: "nope", request_id: "req-1" } } };
      if (c.method === "GET" && c.path.startsWith("/v1/")) return { body: { invoices: [], payments: [], subscriptions: [], plans: [], entries: [], lots: [] } };
      return undefined;
    });
    await gw.install(page);
    await signInAsOperator(page);
    await page.evaluate(() => window.localStorage.setItem("payrudder.locale", "tr"));
    await page.goto("/tenants/0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e/customers");
    await expect(page.getByTestId("error-title")).toHaveText("Aradığınızı bulamadık");
    await expect(page.getByTestId("error-retry")).toContainText("Tekrar dene");
  });
});

test.describe("fields", () => {
  test("inputs, the search box, selects and the filter chips share one fill", async ({ page }) => {
    await openComponents(page);
    const fill = (testid: string) => page.getByTestId(testid).evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(await fill("date-range")).not.toBe("rgba(0, 0, 0, 0)");
    const single = await page.getByLabel("One choice").locator("xpath=..").evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(single).toBe(await fill("date-range"));
    const chip = await page.getByRole("button", { name: "Status", exact: true }).evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(chip).toBe(single);
  });

  test("a sheet keeps its corners clean and its scroll to itself", async ({ page }) => {
    await baseTenant(new MockGateway()).install(page);
    await openTenantPage(page, "/customers");
    // The sheet is the one primitive these rules apply to; the customer form is the sheet every tenant has.
    await page.getByTestId("customers-new").click();
    const sheet = page.locator('[data-slot="sheet-content"]');
    await expect(sheet).toBeVisible();
    expect(await sheet.evaluate((el) => getComputedStyle(el).overflow)).toBe("hidden");
    expect(await sheet.evaluate((el) => getComputedStyle(el).overscrollBehaviorY)).toBe("contain");
  });
});
