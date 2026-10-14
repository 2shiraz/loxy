# Loxy UI — design brief

**Surface mode:** Operate. People come here to put a service online, see whether traffic flows, and fix whatever is red. Everything serves that loop.

## Direction: "Patch panel"

A calm, precise traffic console. The vocabulary comes from the rack: cool porcelain and graphite surfaces, one **fibre-aqua** signal colour (the jacket colour of OM3 fibre patch cords) that means *traffic and action*, and status colours (green, amber, red) reserved strictly for health. Type is a plain technical grotesk for UI and a monospace only for things a person would type into a terminal: addresses, ports, hostnames, config.

The one memorable element is the **route strip** on a project's overview: entry points → Loxy → targets, drawn as an actual route whose flow moves only while requests are flowing, labelled with live req/s. It answers "how do I reach it, and is it working?" in a single glance. Everything around it is quiet.

Rules we hold ourselves to:
- Plain words. "Targets: the servers that receive traffic." No internal jargon on primary screens; HAProxy terms only in advanced places.
- Every empty state names the next step and offers the button for it.
- Every URL, hostname and DNS value has a copy button.
- Live, not twitchy: values update in place, tween briefly, never shift layout (tabular numerals, reserved widths).
- No eyebrows, no gradient text, no decorative glass, no hero-metric template, no card-in-card.

## Colour tokens

| Token | Light | Dark | Use |
|---|---|---|---|
| `--bg` | `#F3F5F6` | `#0A1013` | page |
| `--surface` | `#FFFFFF` | `#10181C` | panels |
| `--surface-2` | `#EDF0F2` | `#162126` | hover, wells, segmented controls |
| `--surface-3` | `#E2E7EA` | `#1D2A30` | pressed, skeleton |
| `--border` | `#E0E5E8` | `#1E2B31` | hairlines |
| `--border-strong` | `#CBD3D8` | `#2C3B42` | inputs |
| `--text` | `#0E1A1F` | `#E3EBEE` | primary ink |
| `--text-2` | `#3B4A51` | `#B3C2C8` | secondary |
| `--muted` | `#5D6C73` | `#8394A0` | tertiary (AA on bg and surface) |
| `--accent` | `#0B6F80` | `#47C6D8` | fibre aqua: primary action, live flow, focus |
| `--ok` | `#267436` | `#4CC273` | healthy |
| `--warn` | `#9A5B00` | `#E8A63A` | degraded, applying |
| `--err` | `#C2333A` | `#F06A6E` | unhealthy, failed |

All text tokens are ≥ 4.5:1 on `--bg`, `--surface` and `--surface-2` in both themes. Status is never colour-only: every status also has a shape or an icon and a word.

## Type

- **Hanken Grotesk** (variable, self-hosted, OFL) for all UI text: a plain, slightly technical grotesk with open apertures that stays legible at 12–13 px.
- **JetBrains Mono** (variable, self-hosted, OFL) for addresses, ports, hostnames, selectors, config and IDs only.
- Scale (ratio ≈ 1.2, base 14 px): `--fs-xs 12` · `--fs-sm 13` · `--fs-md 14` · `--fs-lg 16` · `--fs-xl 19` · `--fs-2xl 23` · `--fs-3xl 28`.
- Weights: 400 body, 500 labels/controls, 600 headings, 650 page titles. Tracking −0.02em on ≥ 23 px, 0 otherwise.
- Numbers: `font-variant-numeric: tabular-nums` everywhere a value can change.

## Space, shape, depth

- 4 px grid: `--sp-1 4` · `--sp-2 8` · `--sp-3 12` · `--sp-4 16` · `--sp-5 20` · `--sp-6 24` · `--sp-8 32` · `--sp-10 40`.
- Radii: controls 8 px, panels 12 px, pills only for small badges.
- Depth: panels use a 1 px border and no shadow. Only floating layers (menus, dialogs, toasts, the mobile sheet) cast a shadow (`--shadow-pop`, offset + soft blur).

## Motion

- Curves: `--ease-out cubic-bezier(.23,1,.32,1)` for anything entering or responding; `--ease-drawer cubic-bezier(.32,.72,0,1)` for sheets.
- Durations: press 120 ms (`scale(.97)`), hover/colour 120 ms, popovers 160 ms, dialogs/sheets 220 ms. Nothing over 300 ms.
- Live values tween for 450 ms with ease-out; the route strip's flow is the only ambient motion and stops at 0 req/s.
- `prefers-reduced-motion: reduce`: no tweening, no flow, no slide; opacity and colour only.
- Keyboard-initiated actions are never animated.

## Layout & breakpoints

- **≥ 1100 px** desktop: 232 px sidebar (collapsible to a 64 px rail, remembered per browser) + content up to 1240 px.
- **700–1099 px** tablet: icon rail by default; tables stay tables.
- **< 700 px** phone: no sidebar; a bottom tab bar (Home, Projects, Domains, Activity, More) with a sheet for the rest. Tables marked `.tbl.cards` collapse into stacked cards; other wide tables scroll inside their own panel, never the page. Touch targets ≥ 40 px.
- Side gutter 16 px on phones, 28 px from tablet up.

## Live

One SSE connection (`/api/v1/events`). Every 2 s `live.tick` carries status, health and traffic for every project plus node apply versions. Pages read it through `$lib/stores/live.svelte` and overlay it onto fetched data; a changed `health_sig`, `version`, `status` or `maintenance` triggers a targeted refetch of that project only. The top bar shows **Live** / **Reconnecting** / **Offline**, plus **Applying…** while the node's desired version is ahead of the applied one.
