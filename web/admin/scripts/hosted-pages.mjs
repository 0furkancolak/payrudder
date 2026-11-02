// Looks at the customer-facing pages (services/gateway/internal/httpapi): screenshots them and runs axe-core on them, in light and dark, at desktop, tablet and phone width.
// The gateway test writes every page in every language as a file:
//
//   HOSTED_PAGES_DIR=/tmp/pages CGO_ENABLED=0 go test ./internal/httpapi -run TestEveryPageRendersInEveryLanguage     (from services/gateway)
//   AXE_CORE=/tmp/axe/node_modules/axe-core/axe.min.js node scripts/hosted-pages.mjs /tmp/pages --axe               (from web/admin; npm i --prefix /tmp/axe axe-core, nothing is added to package.json)
//   node scripts/hosted-pages.mjs /tmp/pages --shots /tmp/shots --only '^(en|ar)-(invoice|portal)$'
//
// It needs Chrome (the same `channel: 'chrome'` the mock e2e uses). Pages are opened as files, so the Content-Security-Policy header is not in play; the gateway test
// checks that. Exit code 1 when axe finds a violation or a page scrolls sideways.
import { readdirSync, mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { createRequire } from "node:module";
import { chromium } from "@playwright/test";

const args = process.argv.slice(2);
const opt = (name) => (args.includes(name) ? args[args.indexOf(name) + 1] : undefined);
const dir = args.find((a, i) => !a.startsWith("--") && !["--shots", "--only"].includes(args[i - 1]));
const shots = opt("--shots");
const only = opt("--only") ? new RegExp(opt("--only")) : undefined;
if (!dir || (!shots && !args.includes("--axe"))) {
  console.error("usage: hosted-pages.mjs <pages dir> [--axe] [--shots <out dir>] [--only <regexp on the page name>]");
  process.exit(2);
}
let axeSource = "";
if (args.includes("--axe")) {
  const path = process.env.AXE_CORE ?? createRequire(import.meta.url).resolve("axe-core/axe.min.js");
  axeSource = readFileSync(path, "utf8");
}
if (shots) mkdirSync(shots, { recursive: true });

const pages = readdirSync(dir)
  .filter((f) => f.endsWith(".html"))
  .map((f) => f.slice(0, -5))
  .filter((n) => !only || only.test(n))
  .sort();
const widths = [
  { w: 1440, h: 900 },
  { w: 768, h: 1024 },
  { w: 360, h: 780 },
];
const schemes = ["light", "dark"];
const browser = await chromium.launch({ channel: "chrome" });
let problems = 0;
for (const scheme of schemes) {
  for (const { w, h } of widths) {
    const ctx = await browser.newContext({ viewport: { width: w, height: h }, colorScheme: scheme });
    const page = await ctx.newPage();
    for (const name of pages) {
      await page.goto("file://" + join(dir, name + ".html"));
      if (shots) await page.screenshot({ path: join(shots, `${name}-${scheme}-${w}.png`), fullPage: true });
      // A page wider than the screen scrolls sideways.
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      if (overflow > 0) {
        problems++;
        console.log(`OVERFLOW ${name} ${scheme} ${w}px: ${overflow}px wider than the screen`);
      }
      if (!axeSource) continue;
      await page.evaluate(axeSource);
      const result = await page.evaluate(() =>
        axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"] } }),
      );
      for (const v of result.violations) {
        problems++;
        console.log(`AXE ${name} ${scheme} ${w}px: ${v.id} (${v.impact}) ${v.help} -> ${v.nodes.slice(0, 3).map((n) => n.target.join(" ")).join(" | ")}`);
      }
    }
    await ctx.close();
  }
}
await browser.close();
console.log(`${pages.length} pages x ${schemes.length} schemes x ${widths.length} widths: ${problems} problem(s)`);
process.exit(problems ? 1 : 0);
