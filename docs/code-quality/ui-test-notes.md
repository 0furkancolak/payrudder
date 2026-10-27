# Admin console manual UI test notes

Date: 2026-10-27. Console `http://localhost:42301`, gateway `:42800`, Mailpit UI `:42825` (the `:42025` port is SMTP). Signed in as the dev seed
`user1@example.com` (credentials in `docs/10-local-dev.md`). Driven with Playwright in Chromium; default UI language Turkish (also spot-checked
en, de, ar). No source was edited and nothing was committed.

Coverage: every page under `/app/**` (app, tenant `/app/t/{id}/**`, org), the operator pages (`/tenants/{id}/**` for the sandbox and the live
tenant, `/organisations`, `/dead-letters`, `/playground`), sign-in/sign-up/forgot/reset/verify/invite, the onboarding wizard (fresh account),
create/edit flows for meters, customers, subscriptions (create and change panel), packages, coupons, credit packs and grants, payment links,
webhooks, API keys, members, expenses, tax and retry settings, affiliate program, credit notes. Viewports 1440, 768, 375; light and dark;
tr/en/de/ar (RTL); keyboard focus on sheets.

Results that were clean: no page-level horizontal overflow at 375 or 768 on any page (also in de/ar/en); every locale has all 3478 keys; no
raw i18n keys; no Vue warnings; keyboard focus is trapped in sheets, Escape closes, focus returns to the trigger; the skip link works;
the sandbox reset, key revoke, app delete (type the name) and leave-account confirmations all exist; the sandbox/live switch works from the
header; dark mode has no low-contrast text found by a scripted scan; sheets for customers, subscriptions, invoices, payments, packages,
integrations, new-* forms restore from the URL after a reload.

## Summary

| Severity | Count |
|---|---|
| blocker | 1 |
| major | 3 |
| minor | 14 |
| polish | 12 |

## Test environment side effects (disclose)

- I accidentally redrove the dead letters: a leftover Playwright `dialog` handler accepted the native confirm on `/dead-letters` for
  `invoice.render.dlq`. Before: `invoice.render.dlq` 35. After: `invoice.render.dlq` 0 and `billing-svc.replies.dlq` 37 (the redriven
  commands were processed and their replies dead-lettered). The stack is a dev stack, but note it if those numbers matter.
- Test data created in the sandbox of Storefront (Acme Labs): customer `ui_test_1` with two subscriptions (the first now cancelled by the plan change),
  invoices SBX-2026-000002/3, credit note SBX-CN-2026-000001 (10,50), meter `api_tokens_ui`, package `ui_pkg`, coupon `UITEST10`, credit pack
  "Pack 100" and 50 granted credits, an expense (120,75), the affiliate program (draft, 12,5 %), a revoked API key, a pending invite to
  `invitee-ui@example.com`, one playground tenant. A tax rate was added and removed again. Another account `uitest-wizard@example.com`
  (org "UI Test Org") was created to walk the onboarding wizard. Mailpit got the matching mails.
- The seed docs list `user2@example.com`; signing in as user2 fails (invalid credentials) and `/organisations` lists only Acme Labs, so
  the second seed user is not present in this stack (not retried, to avoid the lockout).
- Unreproduced: once, after a forced logout, the password sign-in form on one Playwright tab did nothing (no request) until a new tab was
  opened; fresh tabs worked every time. Probably the tool, listed only for completeness.

## Findings

### UI-1 (blocker) Opening the Integrations page signs the user out — fixed in #230
- Page: `/app/apps/{appId}/integrations` and `/app/integrations` (also `?env=live`).
- Steps: sign in, click "Entegrasyonlar" in the sidebar (or load the URL).
- Expected: the cards load. Actual: `GET /gw/v1/mail-settings` is sent without `X-Payrudder-Tenant`; the gateway answers 401
  `identity.session.act_as_required`; the console treats any 401 as an ended session, clears the token and goes to `/signin?expired`.
  The person is logged out every time the page is opened. (My later runs blocked that one request with a Playwright route to keep testing.)
- Source: `web/admin/src/features/integrations/MailSheet.vue` (about line 34-36): `liveTenant` is `""` while the sheet is closed, and
  `useMailSettings` in `web/admin/src/features/mail/api.ts` has no `enabled: tenantId !== ""`, so the query runs on page mount. Also affects
  any app whose live tenant is not resolved yet.

### UI-2 (major) Any 401 on a signed-in call is treated as a lost session — fixed in #230
- Page: all. Cause of the severity of UI-1.
- Actual: `endSessionOn401` in `web/admin/src/shared/api/client.ts` only exempts `/gw/admin/` calls. A 401 with key
  `identity.session.act_as_required` (a client bug) or a tenant-scoped call that the gateway refuses is not a dead session.
- Expected: only log out when the key says the session is invalid/expired (or after one failed `/auth/me`).

### UI-3 (major) MRR, ARR and "top packages" ignore the seat count — fixed in #229
- Page: dashboard `/app/apps/{id}` and Reports > Abonelikler (`/app/t/{id}/reports`).
- Steps: create a subscription for package `pro` with 2 seats (fixed fee 999,00 x 2 is invoiced: invoice line "Base fee x 2 seats").
- Expected: MRR +1.998,00 (dashboard 5.593,00, "pro" 4.995,00/ay). Actual: MRR 4.594,00 (+999,00 only), "pro: 4 abone, 3.996,00 / ay";
  the revenue report agrees (4.594,00). Invoices and the Accounting page do count the seats (1.399,97 etc.), so the numbers disagree.
- Source (backend): `services/plans-svc/internal/store/queries/reports.sql` lines ~11-12, `fee` is the plan's base fee without `* s.seats`.

### UI-4 (major) The logo link drops the live environment and lands on sandbox — fixed in #230
- Page: sidebar logo (`href="/app?app={id}"`) while `env=live`.
- Steps: switch to live (stored `payrudder.env.{app}` = live), click the Payrudder logo.
- Expected: the live dashboard. Actual: redirected to `/app/apps/{id}?app={id}&env=sandbox`, silently back to sandbox. In a billing console
  that silently flips environment this is dangerous (someone may think they are reading live data); the Sandbox badge is the only hint.
- Source: the `/app` redirect (`web/admin/src/features/product/routes.ts` / `OrgHome.vue`) does not read the stored env; the sidebar logo link
  in `app/layout/Sidebar.vue` carries no `env`.

### UI-5 (minor) Webhook delivery table is clipped and shows a raw HTML body — fixed in #231
- Page: `/app/t/{id}/webhooks`, endpoint sheet > "Teslimatlar".
- Steps: add endpoint `http://example.com/hook`, "Test gönder", open the endpoint.
- Actual: the "Son yanıt" cell is `405HTTP 405: <!doctype html><html lang="en">...` (status concatenated with the body, markup not truncated or
  escaped nicely); the "Oluşturma" column and the "Yeniden gönder" action are cut off to the right of the sheet (needs a horizontal scroll).
- Expected: status badge, body truncated to one line with a tooltip, all columns visible in the sheet width.
- Source: webhook delivery table in `web/admin/src/features/webhooks/`.

### UI-6 (minor) Unknown ids show an endless "…" or a raw API message — fixed in #230
- `/app/apps/not-a-uuid` and `/app/apps/{valid-looking-unknown-id}`: API returns 404 but the page shows only "…" forever (no error, no not-found).
- `/app/t/does-not-exist/customers`: the error card says "İşlem yapılacak hesap geçerli bir hesap kimliği olmalı." (API wording) above the page
  skeleton.
- `?sheet=customer:nope`: nothing opens and the dead `sheet` param stays in the URL.
- Expected: the not-found component (as `/no-such-page` has) or a clear message.

### UI-7 (minor) Not every detail sheet is URL-addressable — fixed in #231
- Meter detail ("Görüntüle") and coupon detail leave the URL unchanged, so a reload or a shared link loses the sheet (customer, subscription,
  invoice, payment, package, webhook, integration sheets all restore). The payments sheet id is `payment:1`, an index-like value, so it can
  point to another payment later. Source: `features/meters/MetersPage.vue` (DetailSheet without `useSheetTarget`), coupons page.

### UI-8 (minor) Form fields have placeholder-only labels — fixed in #231
- Page: Yeni müşteri sheet (also credit pack, new meter and others use `aria-label` + placeholder).
- Actual: only "Uygulama" and "Fatura adresi" are visible labels; Müşteri kimliği, Ad, E-posta, Şehir... disappear once typed. `aria-label` keeps
  screen readers working but sighted users lose context; compare the coupon sheet, which has real labels (`#coupon-code`).
- Source: `web/admin/src/features/customers/` form.

### UI-9 (minor) Inconsistent validation feedback, stale errors — fixed in #231
- Silent no-op: "Bağlantı oluştur" (payment link) with an empty form does nothing, no message and no focus move; expense "Kaydet" the same.
- Disabled submit without a reason: credit pack "Paketi ekle" (credit -5), sign-in and sign-up (short password: only a grey hint), grant credits.
- Native browser bubbles (`required`) on customer, package, coupon-period fields; custom red text elsewhere; both styles on one page.
- Stale errors: after a server error is fixed, the old message stays while typing (payment link "Böyle bir müşteri yok." next to a new "Gün: ..."
  error; credit note amount; tax rates save shows the old retry error "Her gecikme 1 ile 720..." next to "Oranlar kaydedildi.").
- Duplicate customer id shows only "Zaten var." rendered above the buttons, not at the Müşteri kimliği field.

### UI-10 (minor) Untranslated or raw backend text in the Turkish UI — fixed in #232
- Package with no price: "prices geçerli değil: a plan version needs at least one price, a base fee or a quota".
- Invoice lines: "Base fee (monthly) x 2 seats — pro — 2026-10-27 to 2026-11-27", "Unused time on starter — ..." (English, ISO dates).
- Operator invoice sheet: raw enums `COMPLETED`, saga states `FINALIZED`/`PDF_REQUESTED`, lowercase `ödendi` while the app side says `Ödendi`.
- Payments table provider "Mockpsp"; subscription provider "iyzico"/"paddle" lowercase in the select next to "Yönlendirme kurallarına göre".

### UI-11 (minor) Destructive actions use different patterns — fixed in #231
- Webhook "Sil" uses the browser's native `confirm()`; API key revoke, sandbox reset, app delete, leave use the styled dialog.
- "Secret'ı yenile" rotates the signing secret immediately with no confirmation (the old secret stops working).
- The webhook sheet has no delete, only the row menu has it. Meters and packages cannot be deleted (presumably by design, undocumented in the UI).

### UI-12 (minor) Customer entry is free text in some forms, a picker in others; customer edit is partial — fixed in #231
- New subscription and new payment link take the customer as a typed id (`ui_test_1`); Credits has a searchable picker ("UI Test Müşteri (ui_test_1)").
- Lists show `dev_cus_07`/`ui_test_1` instead of the name (subscriptions, invoices, usage "Müşteri" column), while the Usage > Müşteriler tab and
  the customer sheet show names.
- Customer sheet "Düzenle" is a menu with only "Adresi düzenle" and "Vergi bilgisi"; no way to change name or e-mail (confirm if intended). Update: the API has no endpoint to change name or e-mail (only address and tax), so that stays as is until the backend offers one.

### UI-13 (minor) Subscription change panel — fixed in #232
- The four selects (package, period, when, proration) share one row in the 670 px sheet and are truncated: "Dönem aynı ka", "Ne zaman: değ",
  "Orantılı (varsay".
- After "Değişikliği uygula" (immediate change) the sheet keeps showing the old, now cancelled subscription (2 / 9) with no actions; the new
  subscription is a separate row and the old one gives no pointer to it. Pager count jumps 8 -> 9.
- "Önizle" is disabled until something changes (good) but nothing says why.
- "Şimdi bitir" and "Şimdi bitir, kullanılmayanı kredilendir" are solid red next to harmless buttons; not clicked (confirm behaviour untested).

### UI-14 (minor) Arabic / RTL — fixed in #232
- Sheet pager reads "8 / 1" (should be 1 / 8), dates inside Latin text are mangled (`TRY · 272026/10/` for "27 Oct 2026"): missing `dir="ltr"`/`<bdi>` on
  mixed content. Layout otherwise mirrors correctly, no overflow.

### UI-15 (minor) Navigation state not kept in the URL — fixed in #232
- Tabs: Usage (Genel bakış / Sayaçlar / Müşteriler), Affiliates (6 tabs), Reports reset to the first tab on reload and cannot be shared.
- Credits: the selected customer is not in the URL (only the grant sheet is). Search boxes are not kept either (filters are).
- Opening a subscription from a customer sheet navigates to `/subscriptions?...&sheet=` and drops `env=sandbox` from the query.

### UI-16 (minor) Document title is the same everywhere and says "operator console" — fixed in #230
- Every page (also customer-facing `/app/**` and sign-in) has `<title>Payrudder operatör konsolu</title>` (tr) / "Payrudder-Operator-Konsole" (de). Browser
  history, tabs and screen readers cannot tell pages apart, and a customer sees "operator".

### UI-17 (minor) Payment link sheet after creation — fixed in #231
- After "Bağlantı oluştur" the sheet shows the copy-once link but keeps the whole form and the primary "Bağlantı oluştur" button, so one more click
  creates a second link. Title is "Ödeme bağlantıları" (plural) for a single new link.

### UI-18 (minor) Creating an app at the plan limit — fixed in #231
- A new Free-plan organisation with 1 app: "Uygulama oluştur" stays offered; the full form is filled and only the submit answers "Planınızın uygulama
  limitine ulaşıldı". Currency/time zone are free combobox text (`ZZZ`, `Mars/Phobos` are accepted by the field; not reached the server because of the limit).

### UI-19 (polish) Mobile header (375) — fixed in #232
- The avatar wraps to a second row under the toolbar; the breadcrumb is cut to "Abo…" / "Sto…". The sandbox banner uses ~25 % of the first screen.

### UI-20 (polish) Navigation naming — fixed in #232
- Sidebar "Vergi ve yeniden denemeler" is truncated ("Vergi ve yeniden denem...") and lives at `/pricing`; "Sandbox araçları" stays in the sidebar and works
  in `env=live` (shows a "Sandbox" badge). Settings (org) and "Entegrasyonlar" exist twice in the sidebar (app and org) with the same label.

### UI-21 (polish) Dashboard — fixed in #229
- The date range opens on "Özel" (1 Oct - today) instead of a preset; currency chip reads "TRY KENDİ" (the translation of "own", unexplained);
  chart ticks "₺1,3 B" read like "billion" (B = bin) to non-Turkish readers; card values are vertically misaligned (cards without a badge/spark line
  start lower). "Net gelir 0% Önceki döneme göre değişmedi" on empty accounts is noise.
- Customer sheet stat tiles show a bare `0` (no currency) for spend / open balance / credit balance.

### UI-22 (polish) Accounting inbox panel leaks configuration — fixed in #229
- "Bu kurulumda gelen posta alan adı ayarlanmamış (PAYRUDDER_INBOUND_DOMAIN); yalnızca belirteç gösterilir." with a truncated address `invoices+...@…` that
  cannot be used. Env var names should not be in the UI (dev-only hint at most).

### UI-23 (polish) Remaining English `aria-label` — fixed in #230
- The close button of the key-secret dialog and every styled confirm dialog is `Close` while sheets use `Kapat`.

### UI-24 (polish) Webhook URL scheme — fixed in #231
- The page and sheet say "HTTPS uç noktaları" but `http://example.com/hook` was accepted without a warning (sandbox may allow it; say so).

### UI-25 (polish) Format consistency — fixed in #229
- Dates: `27 Eki 2026` in most tables, `26.11.2026 18:40` in payment links and webhook deliveries, native `dd.mm.yyyy` pickers, `2026-10-01 – 2026-10-27` in range chips.
- Tax rate typed as `20,5` (tr) is stored and shown back as `20.5`.

### UI-26 (polish) Terminology — fixed in #229
- Credit note: heading "Alacak dekontları", button "Yeni iade faturası", sheet "Alacak dekontu düzenle" ("düzenle" = issue, but also edit), KPI "İadeler".
- "Kiracı"/"Kiracının para birimi" (tenant) leaks into customer-facing forms.
- Credit grant reasons (welcome gift, goodwill, promotion) all end up as source "Promosyon" in the lots table.

### UI-27 (polish) Console noise
- `GET /v1/affiliates/program` returns 404 for a tenant without a program (an expected empty state) and logs a red console error on every visit.
  Same for expected 4xx on forms (customer not found), which is fine, but the 404 could be a 200 with `null`.

### UI-28 (polish) API keys — fixed in #229
- New key defaults to the "Yönetici" role (all 24 permissions); a least-privilege default (read-only or usage-only) is safer. Keys cannot be named, so a list
  of several keys is `qk_test_xxxx_••••` rows only.

### UI-29 (polish) Smaller flow notes — fixed in #232
- Members: the sole owner is offered "Ayrıl" (the dialog says an owner can invite you again); the server answer was not tested.
- Onboarding: the wizard returns on the "Her şey hazır" screen if the person leaves before pressing "Panoma git". The new account lands with the wizard
  default currency USD while the time zone is Europe/Istanbul. "1 calls" in the test event line.
- `/signin?expired=1` (typed by hand) shows no notice, only the encoded `?expired=%221%22` does; `/invite` keeps "Davet denetleniyor…" under the error.

### UI-30 (polish) Focus — fixed in #231
- Opening any sheet puts the focus on the app select (single app) rather than the first field (Kod / Müşteri kimliği).
