# Phase 50 — UI primitives and polish

The owner's screenshots (in `/private/tmp/claude-501/-Volumes-DEVNAFRU-Projects-example-quotify/7e089eaf-b586-4c4e-9d67-47a87d14c8c6/images/`: 3 filter bar, 4 date picker, 5 region combobox, 6 user menu, 7 invoice sheet, 8 invoice PDF, 9 customer sheet, 10 portal) are the reference. Delete this file in the closing PR.

## Scope

1. **Combobox** (`shared/ui/combobox`): one multi-purpose component looking like the region dropdown in image 5. Single or multiple selection (chips), searchable, **creatable** (offers "Create 'x'"), shows the first 10 items by default; for a static list filters locally, for an API list takes `load(query, signal)` (awaited, **debounced**, with a loading animation, the answer shown, stale answers dropped, errors shown inline). Keyboard and screen-reader complete. Use it in place of the ad-hoc selects that are really searchable lists (country/region/district, currency, time zone, customer pickers) where it fits.
2. **Multi-select filters**: `FilterSelect` (or a sibling) takes several values; `useTableState` filters become `string[]` with URL sync (`?status=open,paid`); every list page's filters work with it.
3. **Inputs, search and selects share one fill**: a slightly grey white (between page white and muted grey), one token for fill, border and focus ring; search box, `Select`, `FilterSelect`, inputs, date trigger all match (see image 3).
4. **Date range picker**: never overflows the viewport (flip/shift, max-width at 375 px); on touch/mobile it uses the native `<input type="date">` pair and works; presets stay.
5. **Skeletons**: neutral grey, not blue.
6. **Sheets and sidebar**: no shadow bleeding outside the rounded corners (image 7/9); no scroll propagation (`overscroll-behavior: contain`) from a sheet, the sidebar, popovers and dialogs to the page behind.
7. **Row actions**: no dropdown chevron on the actions trigger of tables; the detail affordance is an up-right arrow (`ArrowUpRight`), not an eye.
8. **Page errors**: route-level error and not-found components, an error boundary around pages, API errors shown as a proper state (title, key-translated message, retry, request id), in six languages.
9. **GeoNames**: drop the visible "Place names: GeoNames (CC BY 4.0)" line from the UI (the licence stays in `NOTICE` and `docs/geo-data.md`). Place names lose their suffixes: "Province", "Region", "District", "Governorate", "County"… for regions; "İlçesi"/"ilçesi"/"District" for districts. Do it in `services/gateway/cmd/geoimport` (clean at import, prefer a local-language alternate name where GeoNames has one, e.g. Turkish for TR, English otherwise) and regenerate `geonames.json.gz`; keep a guard test that no served name ends with a suffix. Find a sound approach to names (alternate names, ASCII fallback) and write it in `docs/geo-data.md`.
