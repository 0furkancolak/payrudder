import { expect, test, type Page } from "@playwright/test";

// The operator console against the REAL stack: every tab of a tenant that has money data opens without console errors, failed calls or sideways scroll,
// at desktop and phone width. TENANT=<id> picks the tenant (default: the newest one in the list). SHOTS=<dir> saves screenshots.
const operatorToken = process.env.QUOTIFY_OPERATOR_TOKEN ?? "local-operator-token-change-me-0123456789";
const gateway = process.env.QUOTIFY_GATEWAY_URL ?? "http://localhost:42800";
const shots = process.env.SHOTS;

const tabs = ["", "apps", "customers", "subscriptions", "packages", "pricing", "payments", "reports", "credits", "payment-links", "accounting", "providers", "e-invoice", "mail", "address", "webhooks"];

async function tenantId(): Promise<string> {
  if (process.env.TENANT) return process.env.TENANT;
  const res = await fetch(`${gateway}/admin/v1/tenants?page_size=50`, { headers: { authorization: `Bearer ${operatorToken}` } });
  const list = ((await res.json()) as { tenants: { id: string; environment: string }[] }).tenants;
  return list.find((t) => t.environment === "ENVIRONMENT_SANDBOX")?.id ?? list[0]!.id;
}

async function signIn(page: Page): Promise<void> {
  await page.goto("/operator");
  await page.getByLabel("Operator token").fill(operatorToken);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Tenants" })).toBeVisible();
}

test("every tab of a tenant opens quietly at desktop and phone width", async ({ page }) => {
  test.setTimeout(5 * 60_000);
  const id = await tenantId();
  const problems: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error" || m.type() === "warning") problems.push(`${page.url()} console.${m.type()}: ${m.text()}`);
  });
  page.on("pageerror", (e) => problems.push(`${page.url()} pageerror: ${e.message}`));
  page.on("response", (r) => {
    if (r.status() >= 400) problems.push(`${page.url()} ${r.status()} ${r.request().method()} ${r.url()}`);
  });
  await signIn(page);
  for (const [w, h] of [[1280, 900], [375, 812]] as [number, number][]) {
    await page.setViewportSize({ width: w, height: h });
    for (const tab of tabs) {
      await page.goto(`/tenants/${id}${tab ? "/" + tab : ""}`);
      await page.getByRole("main").waitFor();
      await page.waitForLoadState("networkidle");
      const o = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
      if (o.sw > o.cw) problems.push(`HORIZONTAL OVERFLOW /${tab} @${w}: ${o.sw} > ${o.cw}`);
      if (shots) await page.screenshot({ path: `${shots}/op-${w}-${tab || "overview"}.png`, fullPage: true });
    }
  }
  expect(problems).toEqual([]);
});
