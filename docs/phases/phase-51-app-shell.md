# Phase 51 — App shell: sidebar, app scope, user menu, sandbox switch

Reference images: see phase 50 (6 user menu, 7/9 sheets). Delete this file in the closing PR.

## Scope

1. **An app is always selected.** Entering `/app` lands inside an application (the last used, else the first, else the onboarding of phase 52). The app switcher is at the top of the sidebar. The first entry is **All apps**; the choice is in the address (`?app=<id|all>`, kept by every link inside the app) so every list, dashboard and report can show the data of one app or of all ("all customers", "all invoices"…).
2. **Forms say where they write**: every create/edit form starts with an "App" field (the selected app preselected; "All apps"/organisation-wide is not allowed except where the data is organisation level, e.g. the accounting form offers "General accounting").
3. **Sidebar**: categorised and tidy for the selected app (overview; customers & billing; usage & products; money; developers; …), organisation settings at the very bottom of the app group, **Platform** (operator) last and only for operators. Works collapsed, as a drawer on mobile, and in the operator console.
4. **User dropdown** (image 6) also holds **language** and **theme**; they leave the top bar.
5. **Sandbox ↔ live switch is deliberate**: switching shows a confirmation dialog saying that the current app is not affected, that only sandbox (or live) data will be shown, and that nothing is sent to real providers in sandbox; a visible persistent banner/pill follows. Not a one-click toggle.
6. A **tour button** slot in the page header (the button itself is phase 52).
7. No scroll chaining out of the sidebar (shared with phase 50; coordinate through the same CSS rule).
