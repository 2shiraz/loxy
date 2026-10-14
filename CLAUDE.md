# Loxy — notes for coding agents

Loxy is a lightweight, Hetzner‑style managed load balancer panel on top of HAProxy 3.4 LTS.

**The full build spec is `INSTRUCTIONS.md`. Read all of it before writing code, and treat it as the source of truth.**

Key rules (details in the spec):
- Build in the phases of §19; a phase is done only when its Definition of Done passes in CI.
- Stack is decided (§4): Go + SQLite (modernc) + lego, SvelteKit SPA + uPlot, official `haproxy:3.4.x-alpine` image. No new runtime dependencies without an ADR in `docs/adr/`.
- Never let a config that has not passed `haproxy -c` become live (§7.7). The live config on disk must always describe what HAProxy runs (§3.3).
- Items marked **VERIFY** must be proven by an integration test against the pinned HAProxy image before code relies on them; record results in `docs/haproxy-notes.md`.
- User input never goes raw into `haproxy.cfg` (§14.4).
- Performance budgets in §1.4 are acceptance criteria.
- Open questions in §21: use the bracketed defaults unless the owner says otherwise, and list the choices made in the PR description.

## Design process (frontend)
Use the project skills in `.claude/skills/` for all UI work (spec §13): `frontend-design`, `ui-ux-pro-max`, `emil-design-eng` for direction; follow `web-design-guidelines`; run `critique` → `polish` → `quieter` → `harden` on every screen before calling it done.
