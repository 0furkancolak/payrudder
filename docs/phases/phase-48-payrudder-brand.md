# Phase 48 — Payrudder brand surface

## Input

[payrudder-brand/DESIGN.md](../../payrudder-brand/DESIGN.md) and the logo files: the product is named **Payrudder**.

## Decisions (made, not asked)

1. **Surface rename only.** What people see (console, hosted pages, e-mails, invoice PDFs, docs headings, README) says Payrudder. Go module path (`github.com/0furkancolak/quotify`), proto packages, metric and table names stay `quotify`; renaming them is a large breaking change with no user value. Recorded in a new ADR.
2. **Environment variables:** `PAYRUDDER_*` is accepted everywhere `QUOTIFY_*` is, and wins when both are set; `QUOTIFY_*` keeps working. One helper in `pkg/config`, one test; docs show the new prefix.
3. **API key prefixes** keep `qk_` / `qs_` (live keys are already in use in the dev data and in tests); the brand does not change tokens.
4. **Design tokens**: the CSS variables of DESIGN.md §4 replace the console's palette (`web/admin/src/app/styles.css`); status pills follow §3. The icon rail sidebar is dark in both themes as specified.
5. Logos go to `web/admin/public/brand/`, favicon and PWA icons are wired in `index.html` and the manifest; the wordmark switches with the theme.
6. The `payrudder-brand/` folder is committed as the source of truth and linked from docs.

## Acceptance

- No "Quotify" in any user-visible string except the legal/compat notes (a grep test over `web/admin/src` locale files and e-mail templates).
- Contrast checks from DESIGN.md hold in light and dark; Playwright screenshots at phone/tablet/desktop for sign-in, org home, dashboard.
- Six locales updated; Arabic RTL unchanged in layout.
