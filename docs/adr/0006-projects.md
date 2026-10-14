# ADR 0006 — Projects: simple load balancers with targets, services and domains

Status: accepted · 2026-10-14 · supersedes ADR 0005

## Context
ADR 0005 turned Loxy into an HAProxy management panel whose user objects were
HAProxy's own: frontends (binds, TLS, redirect, routes), backends (algorithm,
health check, sticky, PROXY protocol) and servers. Working with it, the owner
found that model too complex for the everyday job: to publish one app a user
had to understand binds, `*` addresses, default backends, routing rules,
redirect listeners and mode matching. The owner's requirement: **"I want this
to be simple."**

What users want is a load balancer they can create in one step, point at their
servers, and reach either on its own port or on a domain with HTTPS that just
works.

## Decision
- **Project = one load balancer.** A user can have many. Each project has
  Overview, Graphs, Targets, Services, Domains, Logs and Settings, plus an
  algorithm (roundrobin/leastconn/source/random), a maintenance switch
  (http 503 / tcp reject) and labels.
- **Targets** — where traffic goes: an IP (optionally `ip:port`), an inventory
  host, or a label selector over host labels; weight, backup, drain/maintenance.
  Each target is health-checked per service.
- **Services** — ports the project listens on: protocol `http | https | tcp`,
  listen port → destination port, health check (tcp/http/https), sticky
  sessions (http/https), PROXY protocol v1/v2, timeout, certificates (https).
  A new project automatically gets an http service on **its own free port,
  allocated from 20000 upward** (20000–29999); the listen port can be set explicitly. Listen
  ports are unique across all projects. **Ports 80 and 443 are reserved for
  domains**; the panel port, 8405, 5514 and loxy's internal listener port are
  reserved too.
- **Domains** — attach a hostname to a project (optionally to one service;
  default: the project's first http/https service). Traffic for the hostname on
  the shared :80/:443 is routed to the project. **HTTPS is automatic**: once the
  name resolves to the node, Loxy issues a certificate (Let's Encrypt HTTP-01
  through the shared :80, or DNS-01 through a DNS provider) and redirects
  :80 → :443. Required DNS records are the node's addresses; optional automatic
  A/AAAA records via a libdns DNS provider.
- **Compiled to the verified rendering engine.** Users and the API never see
  frontends/backends. Internally each service `S` renders `frontend fe_<S>`
  (`bind 0.0.0.0:<listen>` + `ipv6@:::<listen> v6only` when the host has IPv6;
  `ssl crt-list` for https) with `default_backend be_<S>`, and `backend be_<S>`
  holds the project's targets as servers `s<targetID>` / `s<targetID>x<hostID>`
  (port = target override or service destination port) with the service's
  health check, sticky cookie (`secure` on https) and PROXY protocol. Domains
  render two shared frontends: `sys_http` on :80 (ACME HTTP-01 from
  `virt@acme_http01`, 301 to https for domains whose certificate is issued,
  `use_backend be_<S>` by Host otherwise, 404 for unknown hosts) and `sys_https`
  on :443 once a domain certificate is issued (`ssl crt-list` of domain
  certificates, `use_backend` by Host, `X-Forwarded-Proto https`, 404 for
  unknown hosts). HTTPS is automatic: when a domain verification finds the
  name resolving to the node and `https` is on and no certificate exists yet,
  a managed certificate is issued (HTTP-01, or DNS-01 when the domain has a DNS
  provider); :80 redirects only once it is issued. Maintenance uses the `opt@`
  maint map keyed by service id. Everything verified in ADR 0004 / `docs/haproxy-notes.md`
  is kept: `sys_panel`, `sys_stats`, master-worker `-W`, `crt-base` relative
  crt-lists, named defaults with `from`, runtime ops for target
  add/remove/weight/state, reload otherwise, `haproxy -c` before going live.
- Metrics and graphs per project and per service (from `be_<S>`, so domain
  traffic is included), live logs per project. Alert rules filter by project
  labels; node-wide events (apply, backup) are not filtered.
- Revisions + rollback, users/roles/API tokens/TOTP, notifications,
  backup/restore and the Prometheus endpoint are unchanged.
- API: `/projects`, `/projects/{id}/services`, `/projects/{id}/targets`,
  `/projects/{id}/metrics`, `/domains` (with `project_id`, `service_id`,
  `https`) — see spec §12 and `api/openapi.yaml`.

## Consequences
- Storage: the load-balancer project lives in a new table **`balancers`**
  (foreign keys `balancer_id` on services, targets, target_slots, domains and
  events), because `projects` is the existing tenancy table. The tenancy table
  is unchanged; hosts, certificates, DNS providers, users and API tokens keep
  their `project_id` (always 1), which is why they are usable by every
  load-balancer project. The API and UI call a balancer a "project".
- Migration `0002_projects.sql`: frontend/backend definitions are dropped (they
  do not map 1:1 onto projects and Loxy is pre-1.0) — `routes`,
  `frontend_certificates`, `frontends`, `server_slots`, `backend_servers` and
  `backends` are removed and `metrics_1m` is cleared; domains are copied
  detached (`balancer_id` NULL, `https` 1; attach them to a project
  afterwards); events are copied without their frontend/backend ids. Only
  pre-release databases with a `load_balancers` table are refused, with a clear
  error.
- Deleting a project deletes its domains (their auto-managed DNS records are
  removed first); `ON DELETE SET NULL` only remains as a safety net.
- Frontends, backends, binds, routes and default backends are no longer
  user-visible. The read-only Config view still shows the rendered HAProxy text.
- No per-address binds: services listen on all addresses of the node. Port
  collisions reduce to "listen port unique + not reserved".
- The per-frontend HTTP→HTTPS redirect option and `sys_acme_fe<id>` are
  removed: **:80 redirects only for domains**, through `sys_http`. A service
  port is served exactly as configured (http or https).
- Host/path routing rules are removed from v1; host routing happens only
  through domains. Path routing inside a project is a Phase 4 item.
- A target is rendered in every service backend of its project, so target
  runtime ops multiply by the number of services (still one command per
  backend; no reload).
- Maintenance for http/https services is rendered in `be_<S>` (one rule that covers the service port and domain traffic); tcp services reject in `fe_<S>`.
- Exact-host `use_backend` rules are rendered before wildcard ones. Deleting the
  last http/https service of a project with domains, or switching it to tcp,
  returns 409. `LOXY_PANEL_PORT` 80/443 is refused at start. Automatic ports
  come from 20000–29999.
- Project metrics are a new `pr` scope (sum of the project's service backends,
  so domain traffic is included); `?service_id=` returns that service's backend
  series. Health events are `target.health.changed` with the project id.
- Performance budgets are restated as 50 projects / 200 services /
  1 000 targets.
