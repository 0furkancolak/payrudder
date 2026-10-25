# Payrudder design system

> Payrudder steers every payment to the right route. Usage metering, billing and payment orchestration in one platform.

This file is the source of truth for how Payrudder looks and sounds. It is written for **Tailwind CSS v4** and **shadcn/ui** (React) / **shadcn-vue** (Vue): the tokens use shadcn's names, so every shadcn component picks up the brand with no changes. Anyone who builds UI for Payrudder, people or AI agents, should read this first. When a choice is not covered here, pick the quieter option.

**Rules for agents.**
- Use semantic classes (`bg-primary`, `text-muted-foreground`, `border-border`), never raw hex or Tailwind's default palette (`bg-blue-600`, `text-gray-500`).
- Use the palette classes (`rudder-*`, `ink-*`) only for brand artwork and charts, never for UI states.
- Add a new token to the CSS in section 4 before you use it. Never write arbitrary values like `bg-[#2F58E0]`.
- Install components with the shadcn CLI and style them through tokens and `cva` variants, not by editing colors inside them.

---

## 1. Brand

**Name.** Always written `Payrudder`: one word, capital P, everything else lowercase. Never `PayRudder`, `Pay Rudder` or `PAYRUDDER`. In code, use `payrudder` (packages, modules, env prefix `PAYRUDDER_`).

**Idea.** A rudder does not move the ship. It decides where the ship goes. Payrudder does the same for money: it measures usage, turns it into invoices, and routes each payment to the provider that will get it through.

**Personality.**

| We are | We are not |
|---|---|
| Precise: numbers are exact, states are explicit | Playful, cute, emoji-heavy |
| Calm: quiet surfaces, one accent | Loud gradients, neon, glow |
| Technical but clear: plain words for hard things | Jargon for its own sake |
| Trustworthy: we show the proof (ledgers, logs, states) | Vague promises ("magic", "seamless") |

**Voice.**
- Short sentences. Active voice. Sentence case in every heading, button and label.
- Say what happened and what to do next: "Payment failed: the card was declined. Retry with another card."
- Money and counts are always exact. Never round an amount the customer pays.
- Tagline: **Steer every payment.**

---

## 2. Logo

### The mark: "steered route"

Four blue blocks climb from left to right. Two dim blocks continue flat along the bottom.

- **Dim blocks:** the default route a payment would take without Payrudder.
- **Bright blocks:** the route Payrudder steers it onto, going from light to deep blue as it moves forward.

**Construction.** A 4 × 3 grid. Cell 88, block 74, gap 14, corner radius 18 (as ratios: gap ≈ 19% of the block, radius ≈ 24%). Every block sits exactly on a grid cell: no half steps, no overlaps, no touching blocks.

| Block | (col, row) | On dark | On light |
|---|---|---|---|
| Bright 1 | (0, 2) | `#A9CFFF` | `#7DB2FA` |
| Bright 2 | (1, 2) | `#6FA8FF` | `#4C88F2` |
| Bright 3 | (2, 1) | `#4A84F5` | `#2F66E3` |
| Bright 4 | (3, 0) | `#3058D8` | `#1E47C2` |
| Dim × 2 | (2, 2), (3, 2) | `#4A84F5` at 16% | `#2F66E3` at 12% |

### Files

| File | Use |
|---|---|
| `payrudder-icon.svg` | App icon, avatars, social profile pictures, the sidebar logo slot |
| `payrudder-icon-{1024,512,192,180,32,16}.png` | Stores, PWA manifest (192/512), Apple touch icon (180), favicons |
| `favicon.ico` | Browser tab (contains 32 and 16) |
| `payrudder-mark.svg` | Mark alone on **dark** backgrounds |
| `payrudder-mark-light.svg` | Mark alone on **light** backgrounds |
| `payrudder-wordmark-dark.svg` | Mark + name on **dark** backgrounds |
| `payrudder-wordmark-light.svg` | Mark + name on **light** backgrounds |

The wordmark text is Inter SemiBold, converted to outlines, so the files render the same everywhere with no font installed.

In the app, render the logo as an `<img>` (or an inline SVG component) and switch the file with the theme: `dark:hidden` on the light version and `hidden dark:block` on the dark version.

### Rules

- **Clear space:** keep at least one block's width empty on every side of the mark or wordmark.
- **Minimum size:** icon 16 px; mark 24 px wide; wordmark 96 px wide.
- **Don't:** recolor the blocks individually, add gradients or shadows, rotate or mirror the mark (the route always climbs left to right), remove the dim blocks, put the dark-background version on a light background, or rebuild the name in another font.

---

## 3. Color

### Palette (raw colors; brand artwork and charts only)

| Class | Hex | OKLCH |
|---|---|---|
| `rudder-200` | `#A6CEFF` | `oklch(0.840 0.081 254)` |
| `rudder-300` | `#7DB4FF` | `oklch(0.762 0.123 256)` |
| `rudder-400` | `#5A93FA` | `oklch(0.673 0.164 261)` |
| `rudder-500` | `#3D6FF0` | `oklch(0.581 0.202 265)` |
| `rudder-600` | `#2F58E0` | `oklch(0.519 0.212 266)` |
| `rudder-700` | `#1A43B8` | `oklch(0.437 0.190 265)` |
| `ink-50` | `#F2F5FA` | `oklch(0.969 0.007 261)` |
| `ink-100` | `#E9EDF3` | `oklch(0.945 0.009 258)` |
| `ink-900` | `#11141C` | `oklch(0.192 0.017 269)` |
| `ink-950` | `#0B0D13` | `oklch(0.160 0.013 270)` |

### Semantic roles (what the UI uses)

| Role | Light | Dark | Classes |
|---|---|---|---|
| Page | near-white blue-grey | `ink-950` | `bg-background text-foreground` |
| Card / popover | white | `ink-900` | `bg-card text-card-foreground` |
| Primary action | `rudder-600` | `rudder-300` | `bg-primary text-primary-foreground` |
| Secondary / muted | soft grey-blue | lifted ink | `bg-secondary`, `bg-muted text-muted-foreground` |
| Hover / selected | pale blue | deep blue-grey | `bg-accent text-accent-foreground` |
| Brand highlight | `rudder-300` | `rudder-400` | `bg-brand`, `bg-brand-soft text-brand-strong` |
| Sidebar (icon rail) | `ink-900` (dark in both themes) | darker ink | `bg-sidebar text-sidebar-foreground` |
| Focus ring | `rudder-400` | `rudder-400` | `ring-ring` (shadcn components set this) |

### Rules

- **One accent.** Blue is the only brand color. Green, amber and red appear **only** for status, never as decoration.
- **Contrast:** every text/background pair keeps at least 4.5:1 (3:1 for large text and icons). The values in section 4 are checked against this.
- **Status colors.** Every payment state pairs its color with a text label, never color alone:

| State | Role | Pill classes |
|---|---|---|
| `succeeded`, `paid`, `settled` | success | `bg-success-soft text-success` |
| `pending`, `processing`, `requires_action` | warning | `bg-warning-soft text-warning` |
| `failed`, `declined`, `disputed` | destructive | `bg-destructive-soft text-destructive` |
| `refunded`, `void`, `draft` | muted | `bg-muted text-muted-foreground` |
| `routed`, `retrying` | brand | `bg-brand-soft text-brand-strong` |

---

## 4. Tailwind v4 + shadcn setup

### `components.json`

```json
{
  "$schema": "https://ui.shadcn.com/schema.json",
  "style": "new-york",
  "tailwind": {
    "config": "",
    "css": "src/app/styles.css",
    "baseColor": "neutral",
    "cssVariables": true,
    "prefix": ""
  },
  "iconLibrary": "lucide"
}
```

`"config": ""` is correct for Tailwind v4: there is no `tailwind.config.js`; everything lives in the CSS file below. (shadcn-vue uses the same file with `"$schema": "https://shadcn-vue.com/schema.json"` and `"typescript": true`.)

### `styles.css`

This is the whole theme. It defines every token shadcn expects (including `chart-*` and `sidebar-*`), plus Payrudder's own (`brand`, `success`, `warning`, the `-soft` tints).

```css
@import "tailwindcss";
@import "tw-animate-css";

/* Dark mode follows a .dark class or data-theme="dark" on any ancestor. */
@custom-variant dark (&:where(.dark, .dark *, [data-theme="dark"], [data-theme="dark"] *));

/* Raw palette: brand artwork and charts only. */
@theme {
  --color-rudder-200: oklch(0.840 0.081 254);
  --color-rudder-300: oklch(0.762 0.123 256);
  --color-rudder-400: oklch(0.673 0.164 261);
  --color-rudder-500: oklch(0.581 0.202 265);
  --color-rudder-600: oklch(0.519 0.212 266);
  --color-rudder-700: oklch(0.437 0.190 265);
  --color-ink-50: oklch(0.969 0.007 261);
  --color-ink-100: oklch(0.945 0.009 258);
  --color-ink-900: oklch(0.192 0.017 269);
  --color-ink-950: oklch(0.160 0.013 270);
}

/* Semantic tokens -> Tailwind utilities (bg-primary, text-muted-foreground, ...). */
@theme inline {
  --font-sans: "Inter", ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  --font-mono: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace;

  --color-background: var(--background);
  --color-foreground: var(--foreground);
  --color-card: var(--card);
  --color-card-foreground: var(--card-foreground);
  --color-popover: var(--popover);
  --color-popover-foreground: var(--popover-foreground);
  --color-primary: var(--primary);
  --color-primary-foreground: var(--primary-foreground);
  --color-secondary: var(--secondary);
  --color-secondary-foreground: var(--secondary-foreground);
  --color-muted: var(--muted);
  --color-muted-foreground: var(--muted-foreground);
  --color-accent: var(--accent);
  --color-accent-foreground: var(--accent-foreground);
  --color-destructive: var(--destructive);
  --color-destructive-foreground: var(--destructive-foreground);
  --color-destructive-soft: var(--destructive-soft);
  --color-success: var(--success);
  --color-success-soft: var(--success-soft);
  --color-warning: var(--warning);
  --color-warning-soft: var(--warning-soft);
  --color-brand: var(--brand);
  --color-brand-soft: var(--brand-soft);
  --color-brand-strong: var(--brand-strong);
  --color-border: var(--border);
  --color-input: var(--input);
  --color-ring: var(--ring);
  --color-chart-1: var(--chart-1);
  --color-chart-2: var(--chart-2);
  --color-chart-3: var(--chart-3);
  --color-chart-4: var(--chart-4);
  --color-chart-5: var(--chart-5);
  --color-sidebar: var(--sidebar);
  --color-sidebar-foreground: var(--sidebar-foreground);
  --color-sidebar-primary: var(--sidebar-primary);
  --color-sidebar-primary-foreground: var(--sidebar-primary-foreground);
  --color-sidebar-accent: var(--sidebar-accent);
  --color-sidebar-accent-foreground: var(--sidebar-accent-foreground);
  --color-sidebar-border: var(--sidebar-border);
  --color-sidebar-ring: var(--sidebar-ring);

  --radius-sm: calc(var(--radius) - 4px);   /*  8px: badges, small controls */
  --radius-md: calc(var(--radius) - 2px);   /* 10px: buttons, inputs */
  --radius-lg: var(--radius);               /* 12px: dropdowns, popovers */
  --radius-xl: calc(var(--radius) + 4px);   /* 16px: cards */
  --radius-2xl: calc(var(--radius) + 8px);  /* 20px: dialogs, sheets */

  --shadow-card: 0 1px 2px rgb(16 24 40 / 0.04), 0 10px 28px -12px rgb(16 24 40 / 0.10);
  --shadow-pop: 0 4px 12px rgb(16 24 40 / 0.08), 0 24px 48px -16px rgb(16 24 40 / 0.20);
}

:root {
  color-scheme: light;
  --radius: 0.75rem;

  --background: oklch(0.965 0.007 258);
  --foreground: oklch(0.192 0.017 269);
  --card: oklch(1 0 0);
  --card-foreground: oklch(0.192 0.017 269);
  --popover: oklch(1 0 0);
  --popover-foreground: oklch(0.192 0.017 269);
  --primary: oklch(0.519 0.212 266);
  --primary-foreground: oklch(0.985 0 0);
  --secondary: oklch(0.945 0.012 258);
  --secondary-foreground: oklch(0.27 0.03 266);
  --muted: oklch(0.955 0.008 258);
  --muted-foreground: oklch(0.50 0.02 264);
  --accent: oklch(0.95 0.028 258);
  --accent-foreground: oklch(0.437 0.190 265);
  --destructive: oklch(0.55 0.21 27);
  --destructive-foreground: oklch(0.985 0 0);
  --destructive-soft: oklch(0.955 0.03 25);
  --success: oklch(0.50 0.13 155);
  --success-soft: oklch(0.95 0.045 155);
  --warning: oklch(0.52 0.11 65);
  --warning-soft: oklch(0.96 0.055 85);
  --brand: oklch(0.762 0.123 256);
  --brand-soft: oklch(0.95 0.03 255);
  --brand-strong: oklch(0.437 0.190 265);
  --border: oklch(0.915 0.009 258);
  --input: oklch(0.885 0.011 258);
  --ring: oklch(0.673 0.164 261);

  --chart-1: oklch(0.519 0.212 266);
  --chart-2: oklch(0.762 0.123 256);
  --chart-3: oklch(0.437 0.190 265);
  --chart-4: oklch(0.673 0.164 261);
  --chart-5: oklch(0.840 0.081 254);

  /* The icon rail stays dark in both themes, like the app icon. */
  --sidebar: oklch(0.192 0.017 269);
  --sidebar-foreground: oklch(0.82 0.02 262);
  --sidebar-primary: oklch(0.762 0.123 256);
  --sidebar-primary-foreground: oklch(0.192 0.017 269);
  --sidebar-accent: oklch(0.26 0.025 268);
  --sidebar-accent-foreground: oklch(0.969 0.007 261);
  --sidebar-border: oklch(0.27 0.02 268);
  --sidebar-ring: oklch(0.673 0.164 261);
}

.dark,
[data-theme="dark"] {
  color-scheme: dark;

  --background: oklch(0.160 0.013 270);
  --foreground: oklch(0.969 0.007 261);
  --card: oklch(0.192 0.017 269);
  --card-foreground: oklch(0.969 0.007 261);
  --popover: oklch(0.215 0.018 268);
  --popover-foreground: oklch(0.969 0.007 261);
  --primary: oklch(0.762 0.123 256);
  --primary-foreground: oklch(0.192 0.017 269);
  --secondary: oklch(0.26 0.02 268);
  --secondary-foreground: oklch(0.93 0.01 261);
  --muted: oklch(0.235 0.018 268);
  --muted-foreground: oklch(0.72 0.02 262);
  --accent: oklch(0.29 0.045 262);
  --accent-foreground: oklch(0.93 0.03 255);
  --destructive: oklch(0.70 0.18 25);
  --destructive-foreground: oklch(0.160 0.013 270);
  --destructive-soft: oklch(0.31 0.07 25);
  --success: oklch(0.78 0.14 155);
  --success-soft: oklch(0.30 0.06 155);
  --warning: oklch(0.82 0.12 80);
  --warning-soft: oklch(0.32 0.06 80);
  --brand: oklch(0.673 0.164 261);
  --brand-soft: oklch(0.30 0.06 262);
  --brand-strong: oklch(0.840 0.081 254);
  --border: oklch(1 0 0 / 9%);
  --input: oklch(1 0 0 / 14%);
  --ring: oklch(0.673 0.164 261);

  --chart-1: oklch(0.762 0.123 256);
  --chart-2: oklch(0.581 0.202 265);
  --chart-3: oklch(0.840 0.081 254);
  --chart-4: oklch(0.673 0.164 261);
  --chart-5: oklch(0.437 0.190 265);

  --sidebar: oklch(0.13 0.012 270);
  --sidebar-foreground: oklch(0.78 0.02 260);
  --sidebar-primary: oklch(0.762 0.123 256);
  --sidebar-primary-foreground: oklch(0.160 0.013 270);
  --sidebar-accent: oklch(0.22 0.02 268);
  --sidebar-accent-foreground: oklch(0.969 0.007 261);
  --sidebar-border: oklch(1 0 0 / 7%);
  --sidebar-ring: oklch(0.673 0.164 261);
}

@layer base {
  * {
    @apply border-border outline-ring/50;
  }
  body {
    @apply bg-background text-foreground font-sans antialiased;
  }
}
```

**System theme.** Keep the CSS to the two blocks above. To follow the OS setting, set the class on `<html>` before the first paint (the shadcn way) instead of repeating the dark block inside a media query:

```html
<script>
  const t = localStorage.getItem("theme") ?? "system";
  const dark = t === "dark" || (t === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
</script>
```

**Fonts.** Load Inter and JetBrains Mono with `@fontsource-variable/inter` and `@fontsource-variable/jetbrains-mono`, imported once in the app entry.

---

## 5. Typography

| Role | Classes |
|---|---|
| Display (marketing only) | `text-5xl md:text-6xl font-semibold tracking-tight leading-none` |
| H1 (page title) | `text-2xl font-semibold tracking-tight` |
| H2 (section) | `text-lg font-semibold` |
| H3 (card title) | `text-base font-medium` (shadcn `CardTitle`) |
| Body | `text-sm` (14 px; the default in tables and forms) |
| Small / labels | `text-xs font-medium text-muted-foreground` |
| Code, IDs, keys | `font-mono text-[13px]` |

- **Numbers:** every amount, count and table number uses `tabular-nums`, so columns line up.
- **Amounts:** format with `Intl.NumberFormat` in the viewer's locale (`₺1.250,00`, `€1,250.00`), right-align them in tables (`text-right tabular-nums`), and never cut decimals.
- **IDs:** `inv_…`, `pay_…` and API keys use `font-mono` and get a copy button. Mask secret keys (`sk_live_••••3f9a`).
- Use only weights 400, 500 and 600 (`font-normal`, `font-medium`, `font-semibold`).

---

## 6. Space, radius, elevation

- **Spacing:** Tailwind's 4 px scale. Use `1, 2, 3, 4, 6, 8, 12, 16` (4–64 px). Card padding `p-6`, dense tables `px-3 py-2`, gaps between cards `gap-6`.
- **Radius** (base `--radius: 0.75rem`):

| Element | Class |
|---|---|
| Badges, checkboxes | `rounded-sm` (8 px) |
| Buttons, inputs, selects | `rounded-md` (10 px; shadcn default) |
| Dropdowns, popovers | `rounded-lg` (12 px) |
| Cards | `rounded-xl` (16 px; shadcn `Card` default) |
| Dialogs, sheets | `rounded-2xl` (20 px) |
| Pills | `rounded-full` |

- **Shadows:** only `shadow-card` (cards) and `shadow-pop` (popovers, dialogs). No glow, no colored shadows. In dark mode use the border instead: `shadow-card dark:shadow-none`.
- **Layout:** dark icon rail on the left (`bg-sidebar`, shadcn `Sidebar` with `collapsible="icon"`), content `max-w-7xl mx-auto px-4 md:px-6`.

---

## 7. Components (shadcn)

- **Button.** One `variant="default"` (primary) per view. Secondary actions use `outline` or `ghost`. Destructive actions (refund, void, delete) use `variant="destructive"` **and** an `AlertDialog` that says the amount: "Refund ₺1.250,00 to Acme?"
- **Badge (status pill).** Add these variants to the badge's `cva` and use them for every payment state from section 3:

```ts
variant: {
  default: "bg-primary text-primary-foreground",
  success: "bg-success-soft text-success",
  warning: "bg-warning-soft text-warning",
  destructive: "bg-destructive-soft text-destructive",
  brand: "bg-brand-soft text-brand-strong",
  muted: "bg-muted text-muted-foreground",
}
```

  Always put a word inside, never only a dot.
- **Table / DataTable.** The main surface. Sticky header (`sticky top-0 bg-card`), 44 px rows (`h-11`), amounts `text-right tabular-nums`, the status badge in its own column, row click opens a `Sheet` with the detail.
- **Card.** `bg-card rounded-xl border shadow-card dark:shadow-none`, header `CardTitle` + optional `CardDescription`, content `p-6`.
- **Chart.** shadcn `ChartContainer` with `config` colors `var(--chart-1)` … `var(--chart-5)` in order. Status series use `var(--success)`, `var(--warning)`, `var(--destructive)`. Gridlines `stroke-border`. Line for usage over time, bars for invoice totals.
- **Empty state.** One sentence about what will appear, plus one primary button. Use the dim-block motif as a light illustration (`text-muted-foreground/30`), nothing else.
- **Toast.** `sonner`. Success and error only for results of an action the user just took; never for background events.
- **Icons.** `lucide`, `size-4` inline and in buttons, `strokeWidth={1.75}`.

---

## 8. Motion

- `transition-colors duration-150` for hover and focus. `duration-200 ease-out` for drawers and menus (`tw-animate-css` handles shadcn's enter and exit).
- Do not animate money. A changing balance updates in place without counting up.
- Respect reduced motion: add `motion-reduce:transition-none motion-reduce:animate-none` to anything that moves more than opacity.

---

## 9. Renaming the product

The console reads its name from `BRAND_NAME` (served by `/env.js`). Setting `BRAND_NAME=Payrudder` changes every visible "Quotify" in the UI today, with no code change. The full rename (Go modules, proto packages, docs) is a separate, single PR.
