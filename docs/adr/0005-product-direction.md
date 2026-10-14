# ADR 0005 — From cloud-LB clone to HAProxy management panel

Status: accepted · 2026-10-14

## Context
Phase 0/1 modelled Loxy on a cloud load-balancer console: "load balancer"
objects with sizes/plans, rescaling, prices, traffic quotas, an LB-wide
connection cap, location cards, IP-pool assignment, private-network attach
objects, and provider-specific integrations (inventory sync, failover/floating
IP switching, rDNS). On 2026-10-14 the owner decided that Loxy is instead a
**self-hosted management panel for HAProxy**: a replacement for hand-editing
`haproxy.cfg` and for other HAProxy GUIs, used by people who run their own
HAProxy.

## Decision
- The domain model follows HAProxy itself (spec §2):
  **frontends** (mode, binds incl. `*`, TLS + certificates, HTTP→HTTPS
  redirect, default backend, ordered host/path **routes**, timeouts, maxconn,
  forwarded headers, enabled, maintenance, labels) →
  **backends** (algorithm, port, health check, sticky, PROXY protocol,
  timeouts, labels; usable by many frontends of the same mode) →
  **servers** of one backend (IP, inventory host, label selector; weight,
  backup, admin state), resolved into stable server slots.
  Plus **hosts** (inventory), certificates, domains & DNS (attached to a
  frontend), node addresses, revisions, activity and settings.
- Removed: load-balancer objects, plans/sizes, rescale, prices, traffic
  quotas and the plan-based LB-wide cap stick table, location/network-zone
  cards, IP-pool assignment, private-network attach objects (a frontend binds
  on whatever addresses are listed), provider inventory sync, provider
  failover-IP APIs and rDNS editing.
- No provider-specific integrations. Phase 3 HA is generic: keepalived VRRP
  with virtual IPs. DNS providers stay pluggable through libdns (ADR 0003).
- Rendered names: `fe_<id>`, `fe_<id>_rd`, `be_<id>`, servers
  `t<T>[x<H>]` with `id` = slot id, `sys_panel`, `sys_stats`,
  `sys_acme_fe<id>`.

## Consequences
- Simpler renderer: no per-LB cap, no per-service frontend/backend pairs;
  one section per object.
- A server belongs to exactly one backend, so runtime ops (add/remove,
  weight, drain, maint) are one command per resolved server instead of being
  multiplied across every service of an LB.
- Routes give host/path routing already in Phase 1 (previously Phase 4).
- Validation moves to the frontend/backend boundary: cross-frontend port
  collisions (incl. redirect and ACME :80 listeners), tls ⇒ ≥ 1 certificate,
  routes ⇒ http, frontend/backend modes must match, in-use backends cannot be
  deleted.
- Metrics, events and alert rules are scoped to frontends and backends
  (label filters on their labels).
- Migration: Loxy is pre-release, so the schema is rewritten (new initial
  migration) and no data migration is provided. API, UI routes and tests
  follow `api/openapi.yaml`.
