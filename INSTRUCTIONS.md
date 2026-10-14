# Loxy — a self‑hosted load‑balancer panel for HAProxy

**Build instructions for the implementing agent**

| | |
|---|---|
| Name | **Loxy** — repo `github.com/2shiraz/loxy`, Go module `github.com/2shiraz/loxy`, binary `loxy`, image `ghcr.io/2shiraz/loxy`, env prefix `LOXY_`, DB file `loxy.db` |
| Data plane | **HAProxy 3.4 LTS** (official Docker image `haproxy:3.4.x-alpine`, patch version pinned) |
| Control plane | Single Go binary with embedded web UI, SQLite, no other services |
| Purpose | Run simple **load balancers** on your own servers. A **project** is one load balancer: it sends traffic to **targets**, listens on **services** (ports) and can be reached on **domains** with automatic HTTPS. Every change is validated (`haproxy -c`), versioned as a revision, applied without dropping traffic (runtime API or hitless reload) and observable (health, graphs, logs, activity). |
| Spec date | 2026‑10‑14 (product model: ADR 0006, which supersedes ADR 0005) |

---

## 0. How to use this document

1. Read the whole file before writing code. Sections 1–7 are **decisions and contracts**, 8–14 are **feature specs**, 15–19 are **deployment, testing and the phased roadmap**.
2. Keywords: **MUST** = required, **SHOULD** = strong default (deviate only with a written reason in `docs/adr/`), **MAY** = optional.
3. Build in the phases of §19. A phase is done only when its Definition of Done (DoD) is green in CI.
4. Items tagged **VERIFY** are HAProxy behaviours that must be proven by an automated integration test against the pinned HAProxy image *before* code relies on them. If the test disproves the statement, the observed behaviour wins: change the code, keep the test, and note it in `docs/haproxy-notes.md`.
5. Do not add runtime dependencies beyond §4 without an ADR. "Lightweight and fast" is a hard requirement, not a preference.
6. Never let a rendered HAProxy config that has not passed `haproxy -c` become the live config (§7.7).
7. The product model is **projects → targets, services, domains** (ADR 0006). HAProxy frontends and backends are an *internal* rendering detail (§2, §7) and never appear as user objects in the UI or the API.

---

## 1. Product summary

### 1.1 What we are building
A self‑hosted control panel + REST API for one or more Linux servers running HAProxy. The owner's guiding requirement: **it must be simple.** The user thinks in load balancers, not in `haproxy.cfg` sections.

- **Projects** — one project is one load balancer. A user can have many. Each project has an **Overview**, **Graphs**, **Targets**, **Services**, **Domains**, **Logs** and **Settings**, plus a load‑balancing **algorithm** (`roundrobin | leastconn | source | random`), a **maintenance** switch (http → 503 page, tcp → connection rejected) and **labels**.
- **Targets** — where traffic goes: an **IP** (optionally `ip:port`), a **host** from the inventory, or a **label selector** over host labels. Each target has a weight, a backup flag and an admin state (ready / drain / maintenance), is added and removed at runtime, and is health‑checked by every service of the project.
- **Services** — the ports a project listens on: protocol `http | https | tcp`, **listen port → destination port**, a health check (`tcp | http | https`), sticky sessions (http/https), PROXY protocol v1/v2, a timeout and, for https, certificates. A new project automatically gets an **http service on its own free port** (from 20000 up); the listen port can also be chosen explicitly. Listen ports are unique across all projects. Ports **80 and 443 are reserved for domains**.
- **Domains** — attach a hostname to a project (optionally to one of its services; default: the project's first http/https service). Traffic for that hostname on the shared ports 80/443 goes to the project. **HTTPS is automatic:** once the name resolves to the server, Loxy obtains a certificate (Let's Encrypt HTTP‑01 through :80, or DNS‑01 through a DNS provider) and redirects :80 → :443. Loxy shows the required DNS records (the node's addresses) and can manage A/AAAA records automatically through a DNS provider (libdns).
- **Hosts** (inventory of machines), **Certificates** (uploaded or managed via ACME), **DNS providers**.
- The panel shows **health** per target, service and project, **live and historical graphs** per project and per service (open connections, connections/s, requests/s, bandwidth, sessions, queue, response time, HTTP status classes, errors), live logs per project and an activity/audit trail.
- Every change is applied to HAProxy automatically, validated, versioned, and **without dropping traffic** (runtime API where possible, hitless reload otherwise).

### 1.2 Goals
1. **Simple:** "create project → add targets → reach it on its port or attach a domain" covers the everyday job. No HAProxy knowledge needed; no `haproxy.cfg` editing.
2. **Minimal install:** `docker compose up -d` with two containers (official HAProxy + `loxy`). No external database, queue, Prometheus or Redis required.
3. **Fast and light:** budgets in §1.4 are acceptance criteria.
4. **Safe:** invalid config can never reach the live path; every applied state is a numbered revision that can be diffed and rolled back.
5. **API‑first:** the UI uses only the public REST API, so everything is scriptable.

### 1.3 Non‑goals (v1)
Sizes/plans, rescaling, prices, billing or traffic quotas; managing NGINX/Apache; Kubernetes ingress; a WAF rule engine; anycast/multi‑region traffic steering; UDP load balancing; provider‑specific cloud integrations (inventory sync, floating‑IP APIs); **running an authoritative DNS server** (Loxy manages records at external DNS providers through their APIs, §8.12; it never answers DNS queries itself); exposing HAProxy frontends/backends or arbitrary HAProxy keywords (Loxy is opinionated; raw config import is a backlog item, §20); per‑address binds (services listen on all addresses of the node).

### 1.4 Performance budgets (acceptance criteria)

| Item | Budget |
|---|---|
| Controller resident memory, idle, 50 projects / 200 services / 1 000 targets | ≤ 60 MB |
| Controller CPU, idle (1 s stats polling) | < 1 % of one core |
| API read p95 (served from in‑memory snapshot) | < 10 ms |
| Write that is applied via Runtime API (e.g. add target), request → active in HAProxy | < 200 ms |
| Write that needs a reload (validate + swap + reload), 50 projects / 200 services | < 1.5 s |
| UI initial load, all JS + CSS gzip | ≤ 150 KB (charts lazy‑loaded on the Graphs tab) |
| Live graph latency (counter change → pixel) | ≤ 2 s |
| Failed client requests during 100 consecutive reloads under a 2 000 rps load test | **0** |
| Failed client requests while adding/removing 50 targets under load | **0** |

---

## 2. Concept mapping (Loxy → HAProxy)

Users see projects, targets, services and domains. The renderer compiles them into the frontend/backend sections that were verified in Phases 0–1 (ADR 0004, ADR 0006).

| Loxy object | Table(s) | HAProxy construct |
|---|---|---|
| Project (name, algorithm, maintenance, labels) | `balancers` (§6 naming) | No section of its own. `algorithm` → `balance` in every `be_<S>` of the project (**always explicit**, §7.9); `maintenance` → one `opt@maint.map` entry per service of the project |
| Service (protocol, listen → destination port, options) | `services` | `frontend fe_<S> from loxy_http|loxy_tcp` with `bind 0.0.0.0:<listen>` (+ `ipv6@:::<listen> v6only` when the host has IPv6, ADR 0004 §9) and `default_backend be_<S>`; `backend be_<S> from loxy_http|loxy_tcp` |
| https service + certificates | `services.protocol = https`, `service_certificates` | `bind … ssl crt-list svc<S>.lst alpn h2,http/1.1` (`crt-base` + relative entries, ADR 0004); targets get plain HTTP (TLS termination); certs hot‑swapped through the Runtime API |
| Health check (protocol, port, interval, timeout, retries, domain, path, response, status codes, TLS verify) | columns on `services` | In `be_<S>`: `option httpchk`, `http-check send/expect`, `check port inter rise fall`, `timeout check`, `check-ssl` |
| Sticky sessions (cookie name, lifetime; http/https) | service columns | `cookie <name> insert indirect nocache httponly [secure] maxlife <n>s` in `be_<S>` (`secure` on https services) + `cookie <server name>` per server |
| PROXY protocol | service column | `send-proxy`/`send-proxy-v2` **and** `check-send-proxy` on the server lines of `be_<S>` |
| Target: IP / host / label selector | `targets`, resolved into `target_slots` | In **every** `be_<S>` of the project: `server s<T>` (IP) or `server s<T>x<H>` (host / selector match) with `id <slot id>`, port = target port override or the service's destination port; added/removed at runtime as dynamic servers |
| Domain (hostname → project / service, https) | `domains` | Shared `frontend sys_http` (:80) and `frontend sys_https` (:443): `use_backend be_<S> if { req.hdr(host),field(1,:),lower -m str <fqdn> }`; ACME HTTP‑01 answers and 301 → https in `sys_http`; domain certificates in `sys_https.lst` |
| Maintenance | `balancers.maintenance` | `opt@maint.map` keyed by service id: http/https → `http-request return status 503` in `be_<S>` (covers the service port and domain traffic); tcp → `tcp-request connection reject` in `fe_<S>` |
| Host (inventory) | `hosts` | Address source for host/selector targets only |
| Labels | JSON column on projects, hosts, certificates | Selectors, list filters and alert‑rule filters only; never rendered |
| Node addresses | `node_addresses` | Not rendered; endpoint URLs and DNS required records for domains |
| Metrics | collector + rollup tables (scopes `fe`, `be`, `pr`) | Deltas of `show stat` counters of `fe_<S>`/`be_<S>`; project (`pr`) = sum of its service backends |
| System | — | `sys_panel` (+ `sys_panel_be`), `sys_stats`, `sys_http`, `sys_https` |

The metric names of the API (`open_connections`, `connections_per_second`, `requests_per_second`, `bandwidth` with `in`/`out` series, plus `sessions`, `queue`, `response_time_ms`, `http_*xx`, `errors`) are stable contract (§10.5).

---

## 3. Architecture

### 3.1 Picture

```
 Browser (SPA) ──HTTPS──► HAProxy "sys_panel" frontend (:8443) ──► loxy (127.0.0.1:9090)
                                                                     │ REST + SSE
┌────────────────────────────── loxy (one Go process) ───────────────▼──────────────────────────┐
│  API handlers ─► Store (SQLite, WAL) ─► State snapshot (immutable, in RAM) ─► Reconciler      │
│        ▲                                                                       │              │
│  SSE hub ◄── Events ◄── Health aggregator ◄── Stats collector ◄── Runtime client◄┤              │
│  ACME (lego) ─────────────────────────────────────────────────────────────────►│              │
│  Notifier · Audit · Log receiver (UDP 127.0.0.1:5514)          Renderer → Validator → RevisionFS│
└───────────────────────────────┬───────────────────────────────────────────┬──────────────────┘
          unix sockets /run/haproxy (master.sock, admin.sock)    files /usr/local/etc/haproxy/{live,revs}
                                ▼                                           ▼
                 HAProxy 3.4 official image, master‑worker, network_mode: host
                                │
   Clients ⇄ :<service port> or :80/:443 (domains) ⇄ HAProxy (project services) ⇄ Targets
```

### 3.2 Process modes (one binary)
`loxy all-in-one` (default; panel + local node agent in one process), `loxy panel` (central control plane, Phase 3), `loxy agent` (runs next to HAProxy on each HAProxy node, Phase 3), plus admin sub‑commands (`loxy admin reset-password`, `loxy backup`, `loxy restore`, `loxy migrate`, `loxy render --dry-run`, `loxy version`).

Phase 1 ships only `all-in-one`, **but** the code MUST talk to the node through a `NodeTransport` interface (in‑process implementation now, network implementation in Phase 3) so multi‑node needs no refactor.

```go
// internal/agent/transport.go
type NodeTransport interface {
    Apply(ctx context.Context, s *NodeState) (*ApplyResult, error) // desired state, versioned
    Stats(ctx context.Context) (<-chan StatsBatch, error)            // 1 s samples
    Runtime(ctx context.Context, cmds []RuntimeCmd) ([]RuntimeResult, error)
    Info(ctx context.Context) (*NodeInfo, error)                     // versions, features, IPs
}
```

### 3.3 Control loop (reconciler)
1. An API write validates input, writes the desired state in **one SQLite transaction**, increments `desired_version` of the affected node, appends an audit row, and publishes a change event.
2. The node's reconciler goroutine (exactly one per node) debounces events (250 ms quiet period, 1 s max wait) and builds an immutable `NodeState` snapshot.
3. It diffs the snapshot against the last *applied* snapshot and produces an **apply plan**: a list of operations, each classified `runtime` or `reload` (§7.6).
4. It **always** renders the full config tree for the new version into a new revision directory and validates it with `haproxy -c` (§7.2, §7.7).
5. If the plan contains any `reload` op → atomically swap `live` → master CLI `reload`. Otherwise → swap `live` (no reload) and execute the runtime ops. If a runtime op fails → fall back to a reload of the same revision.
6. Store a `config_revisions` row (result, duration, HAProxy output), set `applied_version`, emit events.
7. On any failure the previous good revision stays (or is put back) as `live`, the affected projects and System show an `apply_error` banner with the HAProxy output, and a notification fires.

**Invariant:** the live config tree on disk always fully describes the state HAProxy is running, so a cold restart of the HAProxy container reproduces exactly what runtime operations created. Runtime API calls are an optimisation to avoid reloads, never the only record of a change.

**Source of truth:** desired state in SQLite. Observed state (health, counters, process info) lives in HAProxy and is polled.

---

## 4. Tech stack (decided)

| Layer | Choice | Reason |
|---|---|---|
| Data plane | HAProxy **3.4 LTS** (released 2026‑06‑03, supported to 2031), official `haproxy:<3.4.x>-alpine` image, exact patch pinned in `.env` | LTS; dynamic servers *and* (new in 3.4) dynamic backends; synchronous master‑CLI reload with status |
| Controller language | Go (current stable release) | Low memory, single static binary, first‑class ACME library |
| HTTP server | stdlib `net/http` (method+path patterns) with small middleware; handler interfaces generated by `oapi-codegen` from `api/openapi.yaml` (OpenAPI 3.0.3) | Contract‑first, typed, no framework weight |
| Database | SQLite in WAL mode via `modernc.org/sqlite` (pure Go, no CGO); typed queries with `sqlc`; migrations embedded and run at start | Zero ops, microsecond reads |
| ACME | `github.com/go-acme/lego/v5` used as a library | HTTP‑01 and DNS‑01, custom challenge providers, renewal info (ARI) |
| DNS providers | `github.com/libdns/libdns` interfaces + provider modules (ADR 0003) | One small, uniform record API (`GetRecords`/`AppendRecords`/`SetRecords`/`DeleteRecords`, `ListZones`) for many providers; the same credentials drive ACME DNS‑01 through a lego `challenge.Provider` adapter (§8.12) |
| Crypto | `golang.org/x/crypto/argon2` (passwords), AES‑256‑GCM (secrets at rest), TOTP lib for optional 2FA | |
| Logging | `log/slog` JSON to stdout | |
| Frontend | Svelte 5 + SvelteKit with `adapter-static` (SPA mode, `ssr = false`), TypeScript, Vite | Smallest runtime of the mainstream frameworks |
| Charts | uPlot (lazy‑loaded) | ~50 KB, renders tens of thousands of points smoothly |
| API client | `openapi-typescript` + `openapi-fetch` | Types generated from the same OpenAPI file |
| CSS | Plain CSS with design tokens (CSS custom properties); no component library | Bundle size |
| Icons | Inline SVG sprite with only the icons used | |
| Tests | Go `testing` + `testcontainers-go` (real HAProxy), Go fuzzing, Playwright, `oha`/k6 for load | |
| Packaging | Multi‑stage Dockerfile; final stage `FROM haproxy:<same pinned version>-alpine` + the `loxy` binary, so `haproxy -c` runs the identical binary as production | |

**Explicitly rejected:**
- *HAProxy Data Plane API* as the control path — it is a generic, section‑level CRUD API (another process per node). We need a higher‑level opinionated model, metric history, ACME persistence and multi‑node; we'd translate our model into its model anyway. Borrow its ideas (transactions, reload IDs), not the dependency.
- *Roxy‑WI* — broad scope (NGINX/Apache/Keepalived, SSH‑based management), Python stack, heavier install; exactly what we want to avoid.
- *PostgreSQL / Redis / Prometheus as hard dependencies* — operational weight. We expose an optional Prometheus endpoint instead (§10.6).
- *React/Next, chart.js/ECharts* — bundle size.

---

## 5. Repository layout

```
loxy/
├── INSTRUCTIONS.md                  # this file
├── CLAUDE.md                        # short pointer for coding agents → this file
├── README.md                        # user-facing quick start
├── Makefile                         # dev, build, test, gen, lint, release targets (§17)
├── go.mod / go.sum
├── .env.example                     # HAPROXY_VERSION, LOXY_VERSION, LOXY_* settings
├── api/
│   └── openapi.yaml                 # REST contract, single source of truth (§12)
├── cmd/loxy/main.go                  # sub-commands: all-in-one | panel | agent | admin | backup | restore | migrate | render | version
├── internal/
│   ├── app/                         # wiring, lifecycle, graceful shutdown
│   ├── config/                      # env + flag parsing, defaults, validation
│   ├── model/                       # domain types, enums, validation rules (§14.4)
│   ├── store/
│   │   ├── migrations/              # 0001_init.sql, 0002_projects.sql, ...
│   │   ├── queries/                 # sqlc input
│   │   └── db/                      # sqlc output (generated, committed)
│   ├── state/                       # immutable NodeState snapshots, label-selector resolution, target slots, port allocation
│   ├── reconcile/                   # diff, op classification, apply plan, retries
│   ├── haproxy/
│   │   ├── render/                  # text/template renderer + templates/*.tmpl + golden tests
│   │   ├── validate/                # wrapper around `haproxy -c`
│   │   ├── revfs/                   # revision dirs, hard-link reuse, atomic symlink swap, GC
│   │   ├── runtime/                 # stats-socket + master-socket client, command builders, response matchers
│   │   └── stats/                   # `show stat` / `show info` / `show servers state` parsers
│   ├── metrics/                     # collector, delta + reset logic, ring buffers, rollups, queries
│   ├── health/                      # per-check status, aggregation, transitions → events
│   ├── certs/                       # PEM parsing/validation, storage, lego integration, renew scheduler
│   ├── dns/                         # libdns provider registry, lego DNS-01 adapter, domain verifier, record sync (§8.12)
│   ├── api/                         # handler implementations, middleware (auth, CSRF, rate-limit, request-id)
│   ├── auth/                        # sessions, argon2id, API tokens, RBAC, TOTP
│   ├── events/                      # in-process pub/sub + SSE hub
│   ├── audit/
│   ├── notify/                      # webhook, email (SMTP), Telegram, Slack
│   ├── logrecv/                     # UDP syslog receiver + per-project ring buffers (live logs)
│   ├── agent/                       # node-side applier + NodeTransport (in-process now, network in Phase 3)
│   ├── cluster/                     # panel-side node registry, enrollment, transport server (Phase 3)
│   └── webui/                       # go:embed of web/build (precompressed .br/.gz)
├── web/                             # SvelteKit SPA (§13)
│   ├── src/lib/api/                 # generated types + thin client
│   ├── src/lib/components/          # StatBar, StatCard, Donut, Chart, Tabs, Table, Modal, Dropdown, Toast, LabelChips, PortField, ...
│   ├── src/lib/stores/              # session, sse, snapshot cache
│   ├── src/lib/styles/tokens.css
│   └── src/routes/                  # /login, /dashboard, /projects, /projects/new, /projects/[id]/{overview,graphs,targets,services,domains,logs,settings}, /hosts, /certificates, /domains/{,providers,zones}, /activity, /system, /settings/*
├── deploy/
│   ├── docker/Dockerfile
│   ├── compose/compose.yml          # production all-in-one
│   ├── compose/compose.dev.yml      # dev: haproxy + whoami targets + pebble ACME + loxy
│   ├── compose/compose.agent.yml    # Phase 3: node agent only
│   ├── haproxy/bootstrap.cfg        # minimal config written on first start
│   ├── keepalived/                  # Phase 3 templates + notify scripts
│   ├── sysctl/99-loxy.conf
│   └── install.sh
├── test/
│   ├── integration/                 # testcontainers-go against real HAProxy (incl. all VERIFY tests)
│   ├── e2e/                         # Playwright
│   └── load/                        # oha / k6 scripts + reload-under-load harness
└── docs/
    ├── adr/                         # architecture decision records (0006: projects model)
    ├── haproxy-notes.md             # verified behaviours, version quirks
    └── operations.md                # backup, restore, upgrade, break-glass
```

---

## 6. Data model (SQLite)

Conventions: `INTEGER PRIMARY KEY` ids (shown in UI as `#42`), timestamps as unix milliseconds, JSON stored as `TEXT CHECK(json_valid(col))`, `ON DELETE` rules explicit, `PRAGMA foreign_keys=ON`, `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`.

**Naming:** the user‑facing *project* (one load balancer) is stored in the table **`balancers`**, because `projects` is the pre‑existing **tenancy** table (one row, id 1, kept for possible multi‑tenant use). Foreign keys to a load balancer are therefore called `balancer_id`; the API and UI call it `project` / `project_id`. Tenancy‑scoped tables (`hosts`, `certificates`, `dns_providers`, `api_tokens`, `balancers`, `domains`, `events`) keep their `project_id`, which is always 1 in v1 — that is why hosts, certificates and DNS providers are usable by every load‑balancer project.

| Table | Key columns |
|---|---|
| `users` | id, email (unique), password_hash (argon2id), role (`owner|admin|operator|viewer`), totp_secret_enc, disabled, created_at, last_login_at |
| `sessions` | id_hash, user_id, created_at, last_seen_at, expires_at, ip, user_agent |
| `projects` (tenancy) | id, name, created_at — one row (id 1) in v1 |
| `api_tokens` | id, project_id (tenancy), name, prefix, secret_hash, role, expires_at, last_used_at, created_at |
| `nodes` | id, name, mode (`local|remote`), status, haproxy_version, loxy_version, features JSON (`QUIC`, `PROMEX`, …), last_seen_at, desired_version, applied_version |
| `node_addresses` | id, node_id, address, family (4/6), label — the node's own addresses: endpoint URLs (`http://<addr>:<port>`) and DNS required records for domains |
| `node_groups` | id, name, kind (`single|ha_pair`), vip_mode (`none|vrrp`), virtual_ips JSON — Phase 3 |
| `balancers` (API: project) | id, project_id (tenancy, default 1), node_id, name (UNIQUE(project_id, name)), description, algorithm (`roundrobin|leastconn|source|random`, default roundrobin), labels JSON, maintenance, status (`applying|ready|error`), last_error, version, created_at, updated_at |
| `services` | id, balancer_id (`ON DELETE CASCADE`), protocol (`http|https|tcp`), listen_port (**UNIQUE** — across all projects), destination_port, proxy_protocol (`none|v1|v2`), timeout_s (default 60), sticky_enabled, sticky_cookie_name (default `LOXYSTICKY`), sticky_cookie_lifetime_s (default 300), hc_protocol (`tcp|http|https`), hc_port (0 = target port), hc_interval_s, hc_timeout_s, hc_retries, hc_domain, hc_path, hc_response, hc_status_codes JSON, hc_tls_verify, version, created_at. The project's first http/https service (lowest id) is the domain default |
| `service_certificates` | service_id (`ON DELETE CASCADE`), certificate_id (`ON DELETE RESTRICT`), position — PK(service_id, certificate_id) |
| `targets` | id, balancer_id (`ON DELETE CASCADE`), type (`ip|host|label_selector`), ip, host_id (`ON DELETE CASCADE`; the API refuses deleting a directly referenced host, §8.5), label_selector, port (nullable override), use_private_ip, weight (1–256, default 100), backup, admin_state (`ready|drain|maint`), removing, created_at |
| `target_slots` | id (stable HAProxy server `id`), balancer_id, target_id (`ON DELETE CASCADE`), host_id NULL, address, UNIQUE(target_id, host_id, address) — one row per *resolved* target (an IP target has one slot, a host one, a selector one per matching host), so ids survive reloads. The same slot id is used in every `be_<S>` of the project (server ids are unique per backend) |
| `hosts` (inventory) | id, project_id (tenancy), name, ipv4, ipv6, private_ipv4, labels JSON, notes |
| `certificates` | id, project_id (tenancy), name, type (`uploaded|managed`), domains JSON, key_type (`ecdsa256|rsa2048|rsa4096`), challenge (`http01|dns01`), dns_provider_id (→ `dns_providers`), cert_pem, chain_pem, key_pem_enc, fingerprint_sha256, not_before, not_after, status (`pending|issued|renewing|failed`), last_error, renew_after, labels JSON |
| `acme_accounts` | id, directory_url, email, key_pem_enc, registration JSON, eab_kid, eab_hmac_enc |
| `dns_providers` | id, project_id (tenancy), name, kind (registry key), credentials_enc (AES‑256‑GCM JSON), status (`ok|error|unknown`), last_error, last_checked_at, created_at |
| `domains` | id, project_id (tenancy), fqdn (UNIQUE(project_id, fqdn) — i.e. unique in the installation; RFC 1123, lowercase, `*.` allowed only with a DNS provider), balancer_id (nullable, `ON DELETE SET NULL` — a detached domain is kept but not rendered), service_id (nullable, `ON DELETE SET NULL`; NULL = the project's first http/https service), https (default 1), dns_provider_id (nullable), zone (detected apex, e.g. `example.com`), auto_records (default false), ttl (default 300), status (`unknown|ok|wrong_ip|missing|error`), status_detail, observed JSON (last A/AAAA answers), checked_at, certificate_id (nullable; managed automatically or an uploaded cert that covers the fqdn), managed_record_ids JSON, created_at, updated_at |
| `config_revisions` | id, node_id, revision (monotonic), desired_version, sha256, kind (`runtime|reload|rollback|bootstrap`), result (`ok|failed`), duration_ms, haproxy_output, created_by, created_at, pinned |
| `metrics_1m`, `metrics_5m`, `metrics_1h` | scope (`fe` = `fe_<S>` frontend, id 0 = node total; `be` = `be_<S>` service backend; `pr` = project), scope_id, ts, open_conn_avg, open_conn_max, cps, rps, bytes_in, bytes_out, sessions_avg, queue_max, resp_time_ms_avg, http_2xx, http_3xx, http_4xx, http_5xx, errors — PK(scope, scope_id, ts) |
| `events` | id, ts, project_id (tenancy, default 1), balancer_id NULL, kind, severity, message, data JSON — indexes on `ts` and `(balancer_id, ts)` |
| `audit_log` | id, ts, actor_user_id, actor_token_id, ip, action, resource_type, resource_id, before JSON, after JSON |
| `notification_channels` | id, kind (`webhook|email|telegram|slack`), config_enc, enabled |
| `alert_rules` | id, event_kinds JSON, label_filter (label selector matched against **project** labels; node‑wide events such as apply and backup are not filtered by it), channel_id, min_severity, cooldown_s |
| `settings` | key, value JSON |

Indexes on every foreign key, on `events(ts)`, `audit_log(ts)`, metrics PKs. Keep the whole desired state (minus metrics/events/audit) in an in‑memory snapshot rebuilt on change; API reads hit the snapshot, not SQLite.

**Migrations.**
- `0001_init.sql` — the frontend/backend schema of the previous model (ADR 0005). Kept unchanged so existing databases migrate forward.
- `0002_projects.sql` — moves to this model (ADR 0006). The tenancy `projects` table is left unchanged, and hosts, certificates, DNS providers, users, API tokens, ACME accounts, revisions, audit log and settings keep their rows (and their `project_id`). It creates `balancers`, `services`, `service_certificates`, `targets` and `target_slots`; re‑creates `domains` with `balancer_id`, `service_id` and `https` and copies every domain **detached** (`balancer_id`/`service_id` NULL, `https` 1); re‑creates `events` with `balancer_id` and copies the rows without their frontend/backend ids; drops `routes`, `frontend_certificates`, `frontends`, `server_slots`, `backend_servers` and `backends` (frontend/backend definitions are **not** converted — they don't map 1:1 onto projects); and clears `metrics_1m`. Runs after the automatic pre‑migration backup (§16.6).
- Only a database from the **pre‑release cloud‑load‑balancer model** (before ADR 0005, recognised by its `load_balancers` table) is **refused** at start with a clear error telling the operator to start from an empty data directory — never migrated silently.

---

## 7. HAProxy integration (the core)

### 7.1 Process model, sockets, container
- HAProxy runs in its **own container** from the **official image**, `network_mode: host` (services add listeners on arbitrary ports, which Docker port publishing cannot follow, and host networking avoids the userland proxy/NAT hop).
- Start command (override the image entrypoint so the flags are explicit):
  `haproxy -W -db -S /run/haproxy/master.sock,mode,660,gid,1500 -f /usr/local/etc/haproxy/live/haproxy.cfg`
  - `-W` master‑worker; `-db` foreground; `-S` master CLI socket. **VERIFY** the `-S` bind‑option syntax and resulting socket permissions.
- Worker CLI in `global`: `stats socket /run/haproxy/admin.sock mode 660 gid 1500 level admin expose-fd listeners`. In master‑worker mode the master transfers listening sockets to the new worker on reload automatically (internal socketpair); keeping `expose-fd listeners` is harmless and helps manual `-x` use.
- Master CLI is used for `reload` and `show proc` only; everything else goes to the worker socket. (Worker commands can also be sent through the master socket with an `@1` prefix — useful for the agent in Phase 3.)
- The master's **synchronous `reload`** command (available since 2.7) keeps the connection open until the reload finishes and prints `Success=1` or `Success=0`, followed by `--` and the startup log when HAProxy is built with `USE_SHM_OPEN`. Parse it; never fire‑and‑forget `SIGUSR2`.

**Runtime client (`internal/haproxy/runtime`):**
- Unix‑socket client; default one command per connection (simple and robust), 2 s timeout; `reload` timeout 30 s, read until EOF; max 4 concurrent connections per node.
- The 1 s stats poll MAY use a persistent interactive session (`prompt` mode) — **VERIFY** the 3.4 prompt semantics with `help` first; fall back to per‑command connections.
- Payload commands (`set ssl cert`, `add ssl crt-list`, …) use the `<<` heredoc form terminated by an empty line. PEM never contains blank lines; strip trailing whitespace. Respect `tune.cli.max-payload-size` (3.4) for large chains.
- The CLI answers in free text. Each command builder MUST declare its expected success output (e.g. `New server registered.`, `Backend published.`, empty output for `enable server`) and treat anything else as an error with the full text attached. Keep these matchers in one table with tests.
- Every argument that reaches the CLI is built from validated identifiers (§14.4), never from raw user text.

### 7.2 Files and revisions on disk

```
/usr/local/etc/haproxy/               # volume: rw in loxy, ro in haproxy
├── live -> revs/000123               # atomic symlink; haproxy is started with -f live/haproxy.cfg
└── revs/
    ├── 000122/ ...
    └── 000123/
        ├── haproxy.cfg               # rendered with Root=/usr/local/etc/haproxy/live
        ├── haproxy.check.cfg         # same content rendered with Root=<this dir>, used only for `haproxy -c`
        ├── certs/<cert_id>.pem       # leaf + chain + key, mode 0640
        ├── certs/_placeholder.pem    # self-signed, served until a managed cert is issued
        ├── crt-lists/svc<service_id>.lst   # https services; entries relative to crt-base (ADR 0004)
        ├── crt-lists/sys_https.lst   # issued domain certificates, entries relative to crt-base
        ├── maps/maint.map            # loaded with opt@, keyed by service id, updated at runtime too
        ├── maps/panel_allow.lst
        └── errors/503.http, maint.http
/var/lib/haproxy/server-state         # dump of `show servers state`, written before each reload
/run/haproxy/master.sock, admin.sock
```

- The renderer takes a `Root` parameter. The live file references paths under `/usr/local/etc/haproxy/live/…`; `haproxy.check.cfg` is the same template rendered with the revision's own absolute path, so `haproxy -c` validates the *staged* files before they go live.
- **Atomic swap:** create `live.tmp` → `revs/N`, then `rename(live.tmp, live)`, then fsync the directory.
- Unchanged files are **hard‑linked** from the previous revision (cheap, fast).
- Garbage collection keeps the last 20 revisions plus any `pinned` (rollback) ones.
- Runtime‑only changes (target add, cert renewal) still create a new revision so disk always equals memory.

### 7.3 Naming and stable ids
- Per service `S`: frontend `fe_{S}`, backend `be_{S}` (`S` = `services.id`). System sections: `sys_panel`, `sys_panel_be`, `sys_stats`, `sys_http`, `sys_https`.
- Frontend and backend names MUST differ (HAProxy 3.3+ rejects a frontend and backend sharing a name) — guaranteed by the `fe_`/`be_` prefixes.
- Server names: `s{T}` for an IP target, `s{T}x{H}` for a host or label‑selector expansion (`T` = `targets.id`, `H` = `hosts.id`). Server `id` = `target_slots.id` (stable), so `load-server-state-from-file` re‑applies weight/admin/health state after reloads.
- A target is rendered in **every** `be_<S>` of its project, so a target runtime op is one command per resolved slot **per service** of the project.
- Names in HAProxy use ids only; project names, descriptions, labels and domains as display text never appear in the config (domain FQDNs appear only as validated ACL patterns, §14.4).

### 7.4 Rendered config (reference output)

Templates live in `internal/haproxy/render/templates/`. Output MUST be deterministic (projects, services and targets sorted by id, domains by FQDN with exact before wildcard) so revisions diff cleanly. Reference rendering (deviations recorded in ADR 0004 are already applied; the host has IPv6):

- Project **#1 "shop"** (roundrobin): targets #101 IP `10.0.0.11`, #102 label selector `role=web` matching hosts #7 (`10.0.0.12`) and #8 (`10.0.0.13`), #103 IP `10.0.0.14` (backup). Service **#1** http `:20001 → 8080` with an http health check; service **#2** https `:20002 → 8080` with certificate #12, tcp health check and sticky sessions.
- Project **#2 "db"** (leastconn, in maintenance): target #120 IP `10.0.0.41`; service **#3** tcp `:20003 → 5432` with PROXY protocol v2.
- Domains: `shop.example.com` → project #1 (default service #1), https on, certificate #21 issued; `beta.example.com` → project #1, https on, certificate still pending.

```haproxy
# Generated by loxy. DO NOT EDIT. node=1 revision=123 desired_version=456
global
    user haproxy
    group haproxy
    stats socket /run/haproxy/admin.sock mode 660 gid 1500 level admin expose-fd listeners
    stats timeout 30s
    stats calculate-max-counters off          # 3.4: loxy computes maxima itself
    log 127.0.0.1:5514 len 8192 format rfc5424 local0 info
    maxconn 200000                            # LOXY_HAPROXY_MAXCONN
    hard-stop-after 15m                       # bounds old workers holding long connections
    mworker-max-reloads 50
    server-state-file /var/lib/haproxy/server-state
    crt-base /usr/local/etc/haproxy/live/certs
    ssl-default-bind-options ssl-min-ver TLSv1.2 no-tls-tickets
    ssl-default-bind-ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305
    ssl-default-bind-ciphersuites TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256
    ssl-default-server-options ssl-min-ver TLSv1.2
    # chroot auto                             # 3.4 hardening; enable only after the integration suite passes with it

defaults loxy_http
    mode http
    log global
    option httplog
    option dontlognull
    option redispatch
    load-server-state-from-file global
    retries 3
    timeout connect 5s
    timeout client 60s
    timeout server 60s
    timeout http-request 10s
    timeout http-keep-alive 10s
    timeout queue 30s
    timeout tunnel 1h                         # websockets / upgraded connections
    timeout check 10s

defaults loxy_tcp
    mode tcp
    log global
    option tcplog
    load-server-state-from-file global
    timeout connect 5s
    timeout client 60s
    timeout server 60s
    timeout check 10s

# ---------- system ----------
frontend sys_panel from loxy_http
    bind :8443 ssl crt /usr/local/etc/haproxy/live/certs/panel.pem alpn h2,http/1.1
    # rendered only when an allowlist is configured:
    acl panel_allowed src -f /usr/local/etc/haproxy/live/maps/panel_allow.lst
    http-request deny if !panel_allowed
    default_backend sys_panel_be

backend sys_panel_be from loxy_http
    server loxy 127.0.0.1:9090

frontend sys_stats from loxy_http
    bind 127.0.0.1:8405
    http-request use-service prometheus-exporter if { path /metrics }   # only if `haproxy -vv` lists PROMEX
    stats enable
    stats uri /stats
    stats refresh 5s

# ---------- domains (shared :80 / :443) ----------
frontend sys_http from loxy_http
    bind 0.0.0.0:80
    bind ipv6@:::80 v6only
    http-request return status 200 content-type text/plain lf-string "%[path,field(-1,/),map(virt@acme_http01)]" if { path_beg /.well-known/acme-challenge/ } { path,field(-1,/),map(virt@acme_http01) -m found }
    http-request redirect scheme https code 301 if { req.hdr(host),field(1,:),lower -m str shop.example.com }
    option forwardfor
    http-request set-header X-Forwarded-Proto http
    http-request return status 404 content-type text/plain string "Unknown host" unless { req.hdr(host),field(1,:),lower -m str beta.example.com shop.example.com }
    use_backend be_1 if { req.hdr(host),field(1,:),lower -m str beta.example.com shop.example.com }

frontend sys_https from loxy_http
    bind 0.0.0.0:443 ssl crt-list /usr/local/etc/haproxy/live/crt-lists/sys_https.lst alpn h2,http/1.1
    bind ipv6@:::443 v6only ssl crt-list /usr/local/etc/haproxy/live/crt-lists/sys_https.lst alpn h2,http/1.1
    option forwardfor
    http-request set-header X-Forwarded-Proto https
    http-request return status 404 content-type text/plain string "Unknown host" unless { req.hdr(host),field(1,:),lower -m str beta.example.com shop.example.com }
    use_backend be_1 if { req.hdr(host),field(1,:),lower -m str beta.example.com shop.example.com }

# ---------- project 1 "shop" · service 1 (http :20001 -> 8080) ----------
frontend fe_1 from loxy_http
    bind 0.0.0.0:20001
    bind ipv6@:::20001 v6only
    timeout client 60s
    option forwardfor
    http-request set-header X-Forwarded-Proto http
    http-request set-header X-Forwarded-Port %[dst_port]
    http-request return status 503 content-type text/html file /usr/local/etc/haproxy/live/errors/maint.http if { str(1),map(opt@/usr/local/etc/haproxy/live/maps/maint.map) -m found }
    default_backend be_1

backend be_1 from loxy_http
    balance roundrobin
    timeout server 60s
    http-request return status 503 content-type text/html file /usr/local/etc/haproxy/live/errors/maint.http if { str(1),map(opt@/usr/local/etc/haproxy/live/maps/maint.map) -m found }   # only because domains route to be_1
    option httpchk
    http-check send meth GET uri /health ver HTTP/1.1 hdr Host shop.example.com
    http-check expect status 200-399
    http-check expect string 'OK'
    timeout check 10s
    server s101 10.0.0.11:8080 id 101 check inter 15s rise 3 fall 3
    server s102x7 10.0.0.12:8080 id 102 check inter 15s rise 3 fall 3
    server s102x8 10.0.0.13:8080 id 103 check inter 15s rise 3 fall 3
    server s103 10.0.0.14:8080 id 104 check inter 15s rise 3 fall 3 backup

# ---------- project 1 "shop" · service 2 (https :20002 -> 8080) ----------
frontend fe_2 from loxy_http
    bind 0.0.0.0:20002 ssl crt-list /usr/local/etc/haproxy/live/crt-lists/svc2.lst alpn h2,http/1.1
    bind ipv6@:::20002 v6only ssl crt-list /usr/local/etc/haproxy/live/crt-lists/svc2.lst alpn h2,http/1.1
    timeout client 60s
    option forwardfor
    http-request set-header X-Forwarded-Proto https
    http-request set-header X-Forwarded-Port %[dst_port]
    http-request return status 503 content-type text/html file /usr/local/etc/haproxy/live/errors/maint.http if { str(2),map(opt@/usr/local/etc/haproxy/live/maps/maint.map) -m found }
    default_backend be_2

backend be_2 from loxy_http
    balance roundrobin
    timeout server 60s
    cookie LOXYSTICKY insert indirect nocache httponly secure maxlife 300s
    server s101 10.0.0.11:8080 id 101 check inter 15s rise 3 fall 3 cookie s101
    server s102x7 10.0.0.12:8080 id 102 check inter 15s rise 3 fall 3 cookie s102x7
    server s102x8 10.0.0.13:8080 id 103 check inter 15s rise 3 fall 3 cookie s102x8
    server s103 10.0.0.14:8080 id 104 check inter 15s rise 3 fall 3 cookie s103 backup

# ---------- project 2 "db" · service 3 (tcp :20003 -> 5432) ----------
frontend fe_3 from loxy_tcp
    bind 0.0.0.0:20003
    bind ipv6@:::20003 v6only
    timeout client 60s
    tcp-request connection reject if { str(3),map(opt@/usr/local/etc/haproxy/live/maps/maint.map) -m found }
    default_backend be_3

backend be_3 from loxy_tcp
    balance leastconn
    timeout server 60s
    server s120 10.0.0.41:5432 id 120 check inter 15s rise 3 fall 3 send-proxy-v2 check-send-proxy
```

`crt-lists/svc2.lst` contains `12.pem [alpn h2,http/1.1]`; `crt-lists/sys_https.lst` contains `21.pem [alpn h2,http/1.1]`; `maps/maint.map` contains `3 1` (project #2 is in maintenance).

Notes:
- No `master-worker` keyword (deprecated since 3.3; `-W` on the command line is enough). `crt-base` + relative crt‑list entries make each revision validate against its own files (ADR 0004).
- Every service listens on all addresses: `0.0.0.0:<listen>` plus `ipv6@:::<listen> v6only` only when the host supports IPv6 (startup probe or `LOXY_IPV6`, ADR 0004 §9). The same applies to `sys_http` / `sys_https`.
- `sys_http` is rendered whenever at least one domain is attached to a project **or** any managed certificate uses HTTP‑01 (incl. the panel certificate for `LOXY_PANEL_DOMAIN`). Rule order: ACME answer, 301 for https domains whose certificate is issued, `http-request return status 404` for any Host that is not an attached domain, then `use_backend be_<S>` by Host (a redirected domain never reaches it; http‑only domains and https domains still waiting for their certificate do).
- `sys_https` is rendered once at least one attached domain has an issued certificate (a bind with an empty crt‑list is invalid, so no placeholder is needed). Its crt‑list holds the issued domain certificates; clients without SNI get the first entry. Same 404 rule and `use_backend` by Host as `sys_http`.
- Unknown hosts get 404 from `http-request return` (evaluated before `use_backend`), so no extra backend section is needed.
- Host conditions: each FQDN becomes one `-m str` pattern on the lower‑cased Host without port; `*.example.com` becomes `-m end .example.com`. Domains of the same service share one `use_backend` condition (patterns OR‑ed, sorted by FQDN). `use_backend` rules with only exact patterns MUST come before rules with wildcard patterns, so an exact domain of one project wins over a wildcard of another.
- A domain routes to `be_<service_id>` of its service (default: the project's first http/https service by `position`). Domains of a project without an http/https service, and detached domains, are not rendered.
- `maint.map` is an `opt@` map keyed by **service id**: present on disk in the revision **and** updated at runtime with `add map`/`del map`, so maintenance is instant and survives reloads. Turning a project's maintenance on adds one entry per service. http/https services answer 503 from `be_<S>`, which covers the service port and traffic arriving through `sys_http`/`sys_https` (verified, `docs/haproxy-notes.md`); tcp services reject the connection in `fe_<S>`. `virt@acme_http01` is a virtual (memory‑only) map because challenge tokens are ephemeral; the reconciler re‑pushes pending tokens after every reload.
- `timeout client` / `timeout server` are always rendered from the service's `timeout_s` (default 60).
- Targets are rendered in every service backend of their project, with port = the target's port override, else the service's `destination_port`.

### 7.5 Field → directive mapping

**Project fields**

| Field | Rendering |
|---|---|
| algorithm | `balance roundrobin|leastconn|source|random` in every `be_<S>` of the project — rendered **explicitly** |
| maintenance | one `maint.map` entry per service; 503 rule in `be_<S>` for http/https, connection reject in `fe_<S>` for tcp (§7.4) |
| name, description, labels | never rendered |

**Service fields**

| Field | http | https | tcp |
|---|---|---|---|
| protocol | `fe_<S>`/`be_<S>` `from loxy_http` | as http + TLS termination | `from loxy_tcp`; no header injection; use for TLS passthrough |
| listen_port | `bind 0.0.0.0:<p>` (+ `ipv6@:::<p> v6only`) | `… ssl crt-list svc<S>.lst alpn h2,http/1.1` | as http |
| destination_port | default port of every server line | same | same |
| certificates | not allowed | crt‑list `svc<S>.lst` (≥ 1) | not allowed |
| forwarded headers | always: `option forwardfor`, `X-Forwarded-Proto http`, `X-Forwarded-Port %[dst_port]` | same with `X-Forwarded-Proto https` | none |
| timeout_s | `timeout client` in `fe_<S>`, `timeout server` in `be_<S>` | same | same |
| sticky (cookie name, lifetime) | `cookie <name> insert indirect nocache httponly maxlife <n>s`, each server `cookie <server name>` | same + `secure` | not allowed |
| proxy_protocol v1/v2 | `send-proxy` / `send-proxy-v2` **and** `check-send-proxy` on every server line (otherwise targets that require PROXY reject the health check) | same | same |

**Target fields** (rendered once per service of the project)

| Field | Rendering |
|---|---|
| ip / host / label selector | `server s<T>` / `s<T>x<H>` `<address>:<port> id <slot>` — address: IP, host `ipv4` (or `private_ipv4` with `use_private_ip`, else `ipv6`), one server per matching host for selectors |
| port | overrides the service destination port |
| weight | `weight <n>` (omitted when 100) |
| backup | `backup` |
| admin_state | applied at runtime (`set server … state`) and kept across reloads by server‑state (ADR 0004 §6, §8). `render.ServerArgs` (shared with runtime `add server`) never carries it; the backend renderer appends `disabled` to the server line of a target in `maint`, so a cold start keeps maintenance. `drain` is runtime‑only |

**Health check (service)**

| Field | Rendering |
|---|---|
| hc_protocol `tcp` | `check` only (L4 connect) |
| hc_protocol `http` | `option httpchk` + `http-check send meth GET uri <path>` (+ `ver HTTP/1.1 hdr Host <domain>` when domain set; without a domain HAProxy sends HTTP/1.0 without Host) |
| hc_protocol `https` | as `http` plus `check-ssl verify none` on servers; if `hc_tls_verify` → `check-ssl verify required ca-file @system-ca check-sni <domain>` (**VERIFY** `@system-ca` support in the image) |
| hc_port | `check port <p>` (omit when equal to the server port) |
| hc_interval_s | `inter <n>s` |
| hc_timeout_s | `timeout check <n>s` in the backend (must be < interval) |
| hc_retries | `fall <n> rise <n>` (unhealthy after n failures, healthy again after n successes) |
| hc_status_codes | `http-check expect status <list>` — HAProxy accepts comma‑separated codes and ranges (e.g. `200-399`) |
| hc_response | `http-check expect string '<s>'` |

Status‑code conversion (pure function, table‑tested): each item matches `^[1-5][0-9?]{2}$`; `2??` → `200-299`, `20?` → `200-209`, `204` → `204`; merge adjacent ranges (`2??,3??` → `200-399`); de‑duplicate; sort.

**VERIFY (important):** that two consecutive `http-check expect` rules after one `send` are both evaluated against the same response (status AND body). Test: server returns 200 without the string → must be DOWN; returns 500 with the string → must be DOWN; 200 with string → UP. If HAProxy evaluates only one, fall back to a raw `option tcp-check` sequence for that backend (send an HTTP/1.1 request, `tcp-check expect rstring ^HTTP/1\.[01] (2|3)[0-9][0-9]`, then `tcp-check expect string <s>`), and document it in `docs/haproxy-notes.md`.

**Domain fields**

| Field | Rendering |
|---|---|
| fqdn + project/service | Host condition in `sys_http` (until redirected) and `sys_https` → `use_backend be_<S>` |
| https | `sys_https` routing; once the certificate is issued: certificate in `sys_https.lst` and 301 in `sys_http` |
| certificate (managed HTTP‑01) | ACME answers through `virt@acme_http01` in `sys_http` |
| dns_provider, auto_records, ttl | not rendered (DNS only, §8.12) |

### 7.6 Change classification (runtime vs reload)

| Change | Path | Commands / notes |
|---|---|---|
| Add target (any type) when algorithm ∈ {roundrobin, leastconn, random} | runtime | for every service `S` of the project and every resolved slot: `add server be_<S>/s105 1.2.3.4:8080 id 105 check inter 15s rise 3 fall 3 [weight n] [backup] [cookie s105] [send-proxy-v2 check-send-proxy]` → `enable server …` → `enable health …` (dynamic servers start in maintenance with checks off; `default-server` settings are **not** inherited, so render every keyword explicitly; the `enabled` keyword is rejected) |
| Remove target | runtime | per service: `set server <b>/<s> state drain` → wait for `scur=0` on all services or drain timeout (default 30 s) → `set server … state maint` → `wait 5s srv-removable <b>/<s>; del server <b>/<s>` |
| Weight change | runtime | `set server be_<S>/<s> weight <n>` for every service |
| Drain / maintenance / ready | runtime | `set server be_<S>/<s> state drain|maint|ready` for every service |
| Label‑selector match set changes (host labels or addresses edited) | runtime | treated as add/remove |
| Target changes in a `source` (hash) project | **reload** | dynamic servers only work with roundrobin, leastconn, first, random |
| Project maintenance on/off | runtime | `add map /usr/local/etc/haproxy/live/maps/maint.map <service_id> 1` / `del map … <service_id>` per service (+ file in revision). The `opt@` prefix is not part of a map's name, so runtime commands use the plain path; `virt@` *is* part of the name |
| Certificate renewed / replaced (same file) | runtime | `set ssl cert <path> <<\n<pem>\n` → `commit ssl cert <path>` |
| ACME HTTP‑01 token | runtime | `add map virt@acme_http01 <token> <keyauth>` / `del map …` |
| Create / delete project, algorithm change | reload | |
| Add / edit / delete service (any field) | reload | new or changed listeners need a new worker |
| Certificate attached to / detached from an https service | reload | any crt‑list change is reload‑class (ADR 0004); MAY later use `add/del ssl crt-list` |
| Add / delete / move domain, toggle `https`, domain certificate issued (adds the cert to `sys_https.lst` and turns on the 301) | reload | `sys_http`/`sys_https` rules change |
| Panel allowlist | reload (rare) | MAY use `add acl` at runtime |

Every runtime op also produces the new on‑disk revision. If any runtime command fails, the reconciler falls back to a reload of that revision.

HAProxy 3.4 also allows creating, publishing and deleting whole **backends** at runtime (`add backend … from <defaults>`, `publish backend`, `unpublish backend`, `del backend`). v1 doesn't need it (a new service always needs a new listener, which is a reload anyway); MAY be used later.

### 7.7 Reload procedure
1. `show servers state` → write `/var/lib/haproxy/server-state` atomically.
2. `haproxy -c -f revs/N/haproxy.check.cfg` (inside the loxy container, same HAProxy binary). Non‑zero exit → abort, keep current `live`, record the output.
3. Swap `live` → `revs/N`.
4. Master CLI `reload`; read until EOF; require `Success=1`.
5. On `Success=0`: swap `live` back to the previous revision (so a future cold start is safe), record the startup log, mark the affected projects `error`, notify. Old workers keep serving the previous config.
6. Post‑checks: `show proc` lists a new worker; `show stat` contains every expected frontend/backend section; otherwise treat as failed.
7. Re‑push ephemeral runtime state (pending ACME tokens in `virt@acme_http01`).
8. Rate limits: coalesce via the reconciler debounce; at most one reload per second per node; a reload storm queues.
9. Old workers are bounded by `hard-stop-after` (setting, default 15 m) and `mworker-max-reloads` (default 50), so long‑lived connections cannot pile up memory forever.

### 7.8 Bootstrap
On first start loxy writes `revs/000001/` from `deploy/haproxy/bootstrap.cfg` (global, defaults, `sys_panel` with a generated self‑signed `panel.pem`, `sys_stats`) and points `live` at it. The HAProxy container `depends_on` loxy being healthy, so it never starts without a config. On every loxy start: re‑render the current desired state, compare sha256 with `live`, and apply if different (self‑healing after manual tampering).

### 7.9 Known HAProxy gotchas (bake into code and tests)
- The default `balance` changed from `roundrobin` to `random` in HAProxy 3.3 — **always render `balance` explicitly**.
- A frontend and a backend cannot share a name (3.3+).
- `option abortonclose` is on by default for HTTP backends since 3.3.
- Dynamic servers: start in maintenance with checks disabled; don't inherit `default-server`; are not persisted across reloads by HAProxy itself (loxy writes them to the config); the `enabled` keyword is rejected since 3.0; `cookie` is supported since 3.0; only roundrobin/leastconn/first/random backends accept them.
- Counters reset when a new worker starts (§10.3).
- Starting as root: always set `user`/`group` in `global`.
- 3.4 changed compression syntax: `filter comp-res` / `filter comp-req`; `compression direction` is deprecated.
- 3.4 stats page hides the version unless `stats show-version`.
- Native ACME inside HAProxy is still marked experimental and keeps renewed certs in memory only; loxy uses lego instead (§11) and MAY switch later.
- `ipv6@:::<port>` makes the worker exit at startup on hosts without IPv6 although `haproxy -c` passes (ADR 0004 §9).

---

## 8. Feature specification (screens, behaviour, validation, acceptance)

Global navigation: **Dashboard · Projects · Hosts · Certificates · Domains · Activity · System · Settings** (Domains has sub‑pages *Domains*, *DNS providers*, *Zones*). Top bar: global apply status (applying / last error with "Retry", `POST /apply/retry`), user menu, live connection indicator (SSE state).

All destructive actions use a confirm modal; deleting a project requires typing its name. Every mutation returns an **action** object (§12.4) whose progress is shown as a toast and in Activity. An apply error shows a banner on the affected project and on System with the HAProxy output and "Retry" / "Roll back to last good revision".

**Reserved ports** (used by every port validation below): **80 and 443** (domains), the panel port (`LOXY_PANEL_PORT`, default 8443), **8405** (stats), **5514** (log receiver) and the port of `LOXY_LISTEN` (default 9090). `LOXY_PANEL_PORT` set to 80 or 443 is refused at start.

### 8.1 Dashboard (`/dashboard`)
- Stat row (live via SSE): projects (total, by health: healthy / degraded / unhealthy, in maintenance, with apply errors), targets (healthy / total, draining, in maintenance), certificates expiring < 21 d or failed, current open connections and requests/s across all projects, bandwidth in/out.
- **Needs attention** list: unhealthy/degraded projects with the first failing checks ("shop · 10.0.0.12:8080 on :20001 · Layer7 wrong status 503"), projects with apply errors, expiring or failed certificates, domains not resolving correctly.
- Traffic sparkline per top‑5 project (last 5 min), last 10 events, node status (HAProxy version, uptime, last reload result).
- Acceptance: renders from `GET /overview` + one SSE stream in < 100 ms after data arrives; no per‑card polling.

### 8.2 Projects list (`/projects`)
- Cards or table (toggle, remembered per browser): health badge, name, `#id`, **endpoints** (`:20001`, `https://shop.example.com`, "+2", copy on click), protocols (`HTTP`, `HTTPS`, `TCP` badges), targets healthy/total, live open connections sparkline (last 5 min), req/s, maintenance badge, labels.
- Filters: text search, label selector (`env=prod,team!=ops`), health. Sort by name/created/connections.
- **Create project** button → wizard (§8.3). Row ⋯ menu: Open · Maintenance on/off · Copy ID · Delete.
- Empty state explains the model in one line ("A project is a load balancer: it listens on a port or a domain and spreads traffic over your targets.") with "Create project".
- Acceptance: 50 projects render in < 100 ms after data arrives; sparklines update via one SSE stream, not per‑row polling.

### 8.3 Create project wizard (`/projects/new`)
One page, three short steps with a summary; only the name is required. Submitting creates everything in **one transaction → one reload** (`POST /projects` with `targets` and `service`).
1. **Name** (required), description, labels, algorithm (Round Robin default; Least Connections, Source, Random).
2. **Targets** (optional, "Skip for now"): the same add‑targets control as §8.4 Targets (IP list / hosts / label selector).
3. **Service:** protocol `http` (default) | `https` | `tcp` (segmented control); **listen port**: "Automatic" (default; shows the port that will be taken, e.g. "next free: 20003") or a custom value; **destination port** (default 80 for http/https, required for tcp); certificates for https (picker; upload or create managed). Advanced (collapsed): health check, sticky sessions, PROXY protocol, timeout — with the defaults of §8.4 Services.
4. Optional: **Attach a domain** (FQDN; shown only for http/https).
- On success the project Overview opens with its endpoints ("Reachable at `http://203.0.113.10:20003`") and, if a domain was added, its required DNS records.
- Validation: as §8.4 Services/Targets and §8.4 Domains.
- Acceptance: creating a project with only a name gives an http service on the lowest free port in 20000–29999; with 2 IP targets it is reachable and both targets are healthy within 3 s of submit on a dev box; exactly one reload.

### 8.4 Project (`/projects/[id]/…`)
**Header (all tabs):** editable name (inline), health badge, `#id`, endpoints (copy on click), maintenance toggle (confirm), ⋯ menu: Export JSON · Copy ID · Delete.

Tabs: **Overview · Graphs · Targets · Services · Domains · Logs · Settings**. Every tab is a route (`/projects/1/targets`).

**Overview**
- Endpoints card: each service as `protocol listen → destination` with its URL (`http://<node address>:<port>`), each domain as `https://fqdn` (or `http://fqdn` until the certificate is issued) with DNS and certificate status.
- Stat cards (live): open connections, conn/s, req/s, bandwidth in/out.
- **Health** card: donut of healthy targets / total, badge `Healthy | Degraded | Unhealthy | Unknown`, first 3 failing checks with service and reason.
- Targets and services summaries (counts, link to tabs); **Labels** row (chips + editor, §14.4 rules); getting‑started hints while the project has no targets or no domain.

**Graphs**
- Range selector **Live** (rolling 5 min, 1 s points, auto‑updating), 30 min, 1 h, 6 h, 24 h, 7 d, 30 d; filter: whole project / one service.
- Charts: **Open connections**, **Connections per second**, **HTTP requests per second** (http/https), **Bandwidth** (in/out; units auto‑scale B/s → GB/s); "Details" (collapsed): **Sessions**, **Queue**, **Average response time (ms)**, **HTTP responses per second by class** (2xx/3xx/4xx/5xx), **Errors per second** (connection, response, retries/redispatches, denied).
- uPlot with synced cursors; tooltip with exact values and timestamp in the browser's timezone; Live pauses while hovering; no false spikes or dips across reloads (§10.3).
- Acceptance: switching ranges renders in < 150 ms for 30 d; Live updates every second with ≤ 2 s lag; browser CPU < 5 % on a laptop.

**Targets**
- **Add targets** dropdown: **IP** — one or many (paste, one per line, optional `:port`, IPv6 as `[addr]:port`); rejects loopback (unless `LOXY_DEV=true`), link‑local, multicast, unspecified. **Host** — multi‑select from the inventory with search and label filter; "Use private IP" toggle. **Label selector** — input with syntax `key=value`, `key!=value`, `key in (a,b)`, `key` (exists), comma = AND; live preview "matches N hosts" (`GET /label_selector/preview`). Each with optional port override, weight (default 100) and backup flag.
- **Grouped / List** toggle. Grouped: each label selector is a group (tag icon, "N hosts", aggregate health, ⋯); host targets grouped as "Hosts"; IP targets as "IP addresses". List: flat table of resolved targets.
- Row columns: name (host name or "—"), address (port override shown when set), **status** per service (one chip per service: `:20001 healthy`, with check detail tooltip `L7OK/200 in 3 ms` or failure reason; the row status is healthy if healthy on all services, degraded if mixed, unhealthy if on none), weight, backup, sessions, ⋯ menu: Drain · Maintenance · Ready · Set weight · Remove.
- Removal is graceful (drain → maint → delete, §7.6) and shows progress (`removing`).
- Empty state: "No targets yet — add the servers that should receive traffic." with the three add options.
- Validation: ≤ 1 000 targets per installation budget (§1.4); duplicate IP targets (same address and port) within a project refused; a host may be referenced directly only once per project; weight 1–256; port 1–65535.
- Acceptance: adding 25 IP targets to a project with one service results in 25 runtime `add server` ops, **no reload**, all active in < 1 s, zero failed requests under load.

**Services**
- List: protocol badge, `listen → destination`, URL, health check summary (`HTTP GET /health · 15 s`), sticky / PROXY badges, certificates (https), domains routed to it, health (healthy targets / total for this service). Row ⋯: Edit · Delete. **Add service** button.
- Form: protocol `http | https | tcp`; listen port ("Automatic" or custom); destination port; certificates (https; ≥ 1, pending managed allowed — the placeholder is served until issuance); **health check**: protocol (tcp/http/https), port (default = target port), interval (s), timeout (s), retries, domain, path, response, status codes, TLS verify (https checks); **sticky sessions** (http/https): checkbox + cookie name + lifetime (s); **PROXY protocol**: none/v1/v2 with a red warning that enabling it for targets that don't accept PROXY makes the service unreachable; timeout (s). "Show config preview" renders the would‑be `fe_<S>`/`be_<S>` (read‑only `CodeView`).
- Defaults: http, automatic port, destination 80; health check tcp for tcp services and http (`GET /`, status codes `2??, 3??`) for http/https, interval 15 s, timeout 10 s, retries 3; timeout 60 s; sticky cookie `LOXYSTICKY`, lifetime 300 s; PROXY none.
- Validation (server side, mirrored client side):
  - listen port 1–65535, **not reserved** (§8 header) and **unique across all projects** (409 names the project using it: "port 20001 is used by project shop (#1)"); automatic allocation picks the lowest free port in **20000–29999** inside the same transaction ("no free port left" error when exhausted; explicit ports outside the range are allowed).
  - destination port 1–65535.
  - certificates only for https and ≥ 1 there; sticky only for http/https; `hc_timeout < hc_interval`; interval 3–3600; timeout 1–3600; retries 1–10; http/https checks require a path and are allowed on tcp services too; HTTP fields are returned as null for tcp checks; `timeout_s` 1–3600.
  - ≤ 20 services per project.
  - Changing a service to `tcp` or deleting it is refused with `409 conflict` while it is the **only** http/https service of a project that has domains (message lists the domains); domains pinned to a deleted service fall back to the default service.
- Acceptance: adding an https service with a managed certificate (Pebble in CI) serves a valid certificate within 60 s with only one reload; changing the health check path takes effect after one reload.

**Domains**
- The project's domains: FQDN, service (default or pinned), HTTPS on/off, DNS status (**Resolves correctly** / **Wrong IP** / **Missing** / **Unknown**), certificate (issued / pending / failed / none), URL. ⋯: Check now · Edit · Detach · Delete.
- **Add domain**: FQDN (§14.4), service (only http/https services; default "First HTTP service"), **HTTPS** toggle (default on), optional DNS provider (auto‑detected when a connected provider owns a matching zone), "Manage A/AAAA records automatically" (only with a provider; Phase 2). Shows the **required records** immediately (§8.12).
- Validation: FQDN unique across the installation (409 names the project using it); wildcard `*.example.com` only with a DNS provider (DNS‑01 required); the project needs ≥ 1 http/https service; ≤ 100 domains per project.
- Acceptance: see §8.12.

**Logs** — live log stream of this project (sampled, off by default, §20): pause, filter text, service filter, status class filter; columns time, client, service, method + path (http) / bytes, target, status, timings.

**Settings**
- Name, description, labels, algorithm (note that Source makes target changes reload‑class), maintenance mode (explains: http 503 page, tcp connections rejected; applies to ports and domains).
- **Config** (read‑only): rendered sections of this project (`fe_<S>`, `be_<S>` of all services, plus the `sys_http`/`sys_https` lines for its domains) from `GET /projects/{id}/config`, with "Copy" and "Download".
- **Danger zone:** Delete project — confirm by typing the name; removes all services, targets and its domains (reload; auto‑managed DNS records are removed first; `ON DELETE SET NULL` on `domains.balancer_id` remains only as a safety net), keeps metrics for 7 days then purges, audit entry. Certificates are kept.

### 8.5 Hosts (inventory, `/hosts`)
- CRUD of hosts: name, IPv4, IPv6, private IPv4, labels, notes. Bulk import from CSV (`name,ipv4,ipv6,private_ipv4,labels`).
- Shows where each host is used (projects, directly or via a label selector) and its current health there.
- Editing a host's labels or addresses updates every label‑selector / host target at runtime (§7.6). Deleting a host that is referenced directly by a target is refused; selector matches simply drop out (graceful removal).

### 8.6 Certificates (`/certificates`)
- List: name, type (Uploaded/Managed), domains, status (Pending/Issued/Renewing/Failed), expires in (amber < 21 d, red < 7 d), used by (services and domains), labels.
- **Upload**: paste or file for certificate, chain, private key. Validate: key matches cert, not expired, chain completes to a root in the system bundle (warn only), reorder chain if needed. Key stored encrypted at rest.
- **Create managed**: domains (≤ 100), challenge HTTP‑01 (default; the names must resolve to the node — Loxy answers on :80 through `sys_http`) or DNS‑01 (pick a connected DNS provider from §8.12 — credentials are not entered again; required for wildcards), key type ECDSA P‑256 (default) / RSA 2048/4096.
- Certificates created automatically for domains appear here with a "Domain" badge and are deleted with their domain.
- Detail drawer: SANs, issuer, serial, fingerprint, validity, last issuance log, renewal schedule, services/domains using it. Actions: Renew now · Download chain (never the key unless owner role + re‑auth) · Delete (blocked while used by a service or domain).

### 8.7 Domains — global list (`/domains`)
- All domains of the installation: FQDN, project (or **Detached**), service, HTTPS, DNS provider (or "manual"), DNS status, last checked, certificate, auto‑managed records on/off.
- Filters: project, status, detached only. **Add domain** (as §8.4 Domains, with a project picker). Detached domains (e.g. after a project was deleted or after migration `0002`) offer **Attach to project**.
- Sub‑pages *DNS providers* and *Zones* (§8.12).

### 8.8 System (`/system`)
- **Node** card (all‑in‑one shows the local node): status, HAProxy version and feature flags (`haproxy -vv`: QUIC, PROMEX, OpenSSL version), loxy version, HAProxy uptime, reload count and last reload result/duration, current connections, CPU/memory of both containers, IPv6 listener status; warning when the bundled `haproxy -v` differs from the running one.
- **Node addresses**: list of the node's IPv4/IPv6 addresses with an optional label (`POST/DELETE /nodes/{id}/ips`); detected interface addresses are offered for one‑click adding. Used for endpoint URLs and as DNS required records for domains (§8.12). Changing them never changes the HAProxy config by itself.
- **Ports in use**: every listen port with its project and service, plus the reserved ports.
- **Config revisions**: list (revision, kind runtime/reload/rollback/bootstrap, result, duration, author, time, pinned), diff between any two (`CodeView`), "Roll back to this revision" (break‑glass swap + reload of the on‑disk revision, see ADR 0004 §7; the next mutation re‑applies desired state, which the UI states), pin/unpin, HAProxy output of failed applies.
- Phase 3 adds enrollment, multiple nodes and HA groups (§15).

### 8.9 Activity (`/activity`)
- **Events** feed (target/project health transitions, apply/reload results, cert issued/failed/expiring, domain status changes, node offline) with filters by project/kind/severity.
- **Audit log** (who, what, when, before/after diff, IP, token) — read‑only, exportable CSV.

### 8.10 Settings (`/settings/*`)
Users & roles · API tokens · ACME (account email, directory: Let's Encrypt production / staging / custom, EAB) · Notification channels & alert rules (event kinds, label filter on **project labels** — node‑wide events like apply and backup are not filtered, min severity, cooldown, "Send test") · Two‑factor authentication (own TOTP; enforcement for all users) · Panel (domain, port, allowlist, session lifetime) · System (hard‑stop‑after, drain timeout, reload min interval, metrics retention, live‑log sampling) · Backup (schedule, retention, optional S3‑compatible target).

### 8.11 Automatic HTTPS for domains
- A domain with `https` on gets a managed certificate automatically: each verification (`VerifyDomainNow` — on add, "Check now" and the periodic checks of §8.12) that finds the name resolving to the node's addresses issues a managed certificate for the FQDN, if the domain has none yet — HTTP‑01 through `sys_http`, or DNS‑01 when the domain has a DNS provider (required for wildcards) — and links it via `domains.certificate_id`. A domain that already has a certificate is left as is.
- Until the certificate is issued, the domain is served over http on :80 (no redirect); only once it is issued the certificate is added to `sys_https.lst` (rendering `sys_https` if it is the first) and :80 answers `301` to `https://` (one reload). Renewals are runtime (§11.2).
- Failures (rate limits, wrong DNS) show on the domain with the ACME error and retry with the backoff of §11.2; the domain keeps working over http meanwhile.
- `https` off: the domain is served over http on :80 only and no certificate is requested.

### 8.12 DNS: required records, verification, providers, zones

Loxy helps operators point names at their projects and keeps DNS in sync, **without running its own DNS server** (non‑goal, §1.3). All record changes go through the APIs of external DNS providers using the **libdns** ecosystem (`github.com/libdns/*`).

- **Required records** of a domain are the node's addresses (§8.8; public ones only — loopback/private are skipped unless they are the only ones): `A <fqdn> → <ipv4>` / `AAAA <fqdn> → <ipv6>`, TTL 300, with copy buttons. (Phase 3: the HA group's virtual IPs.)
- **Live verification**: Loxy resolves the name (A and AAAA, via the system resolver and, when a provider is attached, also by reading the provider's records) and shows per family: ✓ resolves correctly, ✗ wrong IP (shows the observed addresses), ✗ missing. Runs on add, on "Check now", every 5 minutes for unverified domains and every hour for verified ones, and whenever the node addresses change. Status changes emit `domain.status.changed` events.

**DNS providers** (`/domains/providers`)
- Connect provider accounts through libdns provider modules (the registry's kinds, §21 Q4; each with its own token fields, e.g. a scoped API token with zone‑read and DNS‑edit rights). The registry is a map `kind → {fields schema, constructor}` so new libdns providers are added in one file; `GET /dns_providers` also returns the available kinds and their fields.
- Credentials are encrypted at rest with AES‑256‑GCM (`LOXY_SECRET_KEY`, §14.5), never returned by the API (only `"configured": true`), and tested on save with a `ListZones` call (status `ok|error`).
- **ACME DNS‑01 reuses the same credentials**: `internal/dns` implements lego's `challenge.Provider` (`Present`/`CleanUp`) on top of the libdns `RecordAppender`/`RecordDeleter` interfaces, writing `_acme-challenge.<name> TXT <value>` (TTL 60) into the right zone and deleting exactly that record afterwards. Certificates reference a `dns_provider_id` instead of carrying their own credentials.

**Auto‑managed records** (Phase 2)
- Opt‑in per domain. When on and a provider is connected, Loxy creates the A/AAAA records for the FQDN pointing at the required addresses, **updates** them when the node addresses change, and **removes** them when the domain is detached or deleted or its project is deleted.
- Loxy only touches records it created (their provider IDs are tracked in `domains.managed_record_ids`); pre‑existing records with the same name are reported as a conflict and never overwritten without explicit confirmation.
- Sync runs through the reconciler's event stream (debounced) and is retried with backoff; failures surface as `domain.sync.failed` events.

**Zone editor** (Phase 2, `/domains/zones`)
- List zones from all connected providers; open a zone to list and edit records: types **A, AAAA, CNAME, TXT, MX, CAA, SRV, NS**; columns type, name (relative, `@` for apex), value, TTL; add / edit / delete with confirm.
- Server‑side validation per type: A/AAAA parsed with `net/netip`; CNAME/NS/MX targets are RFC 1123 hostnames; MX priority 0–65535; SRV `priority weight port target`; CAA `flags tag "value"` with tag ∈ `issue|issuewild|iodef`; TXT ≤ 255 chars per string (longer values are split); TTL 60–86400; CNAME cannot coexist with other records of the same name. Records managed by Loxy are marked and edits to them warn.

**Acceptance**
- Adding a domain whose A record points to the node shows "Resolves correctly" within one check; a wrong A record shows "Wrong IP" with the observed address.
- A verified https domain gets a certificate (Pebble in CI) and `http://fqdn` answers 301 → `https://fqdn`, which reaches the project's targets, within 60 s, with at most two reloads (attach + issued).
- Two projects with different domains on the shared :443 each receive only their own traffic; an unknown Host gets 404.
- With a DNS provider connected, a managed DNS‑01 certificate for `*.example.com` is issued (Pebble + a libdns test double in CI) without entering credentials again.
- With auto‑records on, changing a node IPv4 updates the A record within 10 s; deleting the domain removes exactly the records Loxy created.

---

## 9. Health model

Source: `show stat` server rows (`status`, `check_status`, `check_code`, `check_duration`, `check_desc`/`last_chk`, `lastchg`).

| HAProxy server `status` | loxy check status |
|---|---|
| `UP` | healthy |
| `UP n/m` (going down) | healthy (transitioning) |
| `DOWN` | unhealthy |
| `DOWN n/m` (going up) | unhealthy (transitioning) |
| `NOLB` | healthy, not receiving new traffic |
| `MAINT`, `MAINT (via)`, `MAINT (resolution)` | maintenance |
| `DRAIN` | draining |
| `no check` | unknown |

Aggregation: health is checked **per resolved target per service** (one server line per `be_<S>`). A resolved target is `healthy` if healthy on all services, `degraded` if healthy on some, `unhealthy` if on none, `unknown` without checks. A service's health counts its servers; a project is `Healthy` if all active (non‑maintenance) checks are healthy, `Degraded` if some are unhealthy but every service has ≥ 1 healthy target, `Unhealthy` if any service has no healthy target, `Unknown` without targets or services. Transitions produce `target.health.changed` events carrying the `project_id` (stored as `events.balancer_id`), service and check reason, used by notifications; debounce flapping with the alert‑rule cooldown. Optionally enable `option log-health-checks` and parse health transitions from the UDP log stream for sub‑second detection.

---

## 10. Metrics pipeline

### 10.1 Collection
Every 1 s per node: `show stat` (CSV; parse the header line to map columns by name, never by position) and `show info`. `be_<S>` rows carry all traffic of a service (from its own port **and** from domains through `sys_http`/`sys_https`) → service metrics; `fe_<S>` rows add frontend‑only counters (denied/errored requests on the port); server rows → per‑target metrics and health. Project metrics are a separate scope `pr`: the sum of the project's service backends (`be_<S>`), so domain traffic is included. The node total (scope `fe`, id 0) also counts `sys_http`/`sys_https`. One `show stat` call returns everything; it is cheap even with thousands of servers.

### 10.2 Derived series (per service backend; project scope `pr` = sum over its services)

| Series | Derivation |
|---|---|
| open_connections | `be_<S>` `scur` |
| connections_per_second | Δ `be_<S>` `stot` / Δt |
| requests_per_second | Δ `be_<S>` `req_tot` / Δt (http/https only) |
| bandwidth_in / bandwidth_out | Δ `be_<S>` `bin` / Δt, Δ `bout` / Δt (bytes/s) |
| sessions | `be_<S>` / server `scur` |
| queue | `be_<S>` `qcur` (server `qcur`) |
| response_time_ms | `be_<S>` `rtime` (moving average HAProxy maintains), `ttime` |
| http_1xx…5xx per second | Δ `be_<S>` `hrsp_*` / Δt |
| errors per second | Δ `be_<S>` `econ` + `eresp` / Δt; retries `wretr`, redispatches `wredis` |
| denied per second | Δ `fe_<S>` `dreq` + `ereq` / Δt |

### 10.3 Counter resets
New workers start counters at zero. Detect a new worker via `show info` `Pid`/`Uptime_sec` change or any counter decreasing; for that sample use `delta = current` (not negative, not a spike). Connections still served by old workers during drain are not counted — accepted. HAProxy 3.3+ has an experimental `shm-stats-file` that can preserve counters across reloads; MAY be enabled behind a setting after a **VERIFY** test.

### 10.4 Storage and retention
- In memory: ring buffer of 1 s points for the last 60 min per service and per project (≈ 3 600 × ~12 series × 8 B ≈ 350 KB per object at worst; keep per‑target series only for the last 15 min).
- SQLite rollups written in one transaction per minute: `metrics_1m` (scope `fe|be|pr`, scope_id), with 5 m / 1 h resolutions aggregated on read or written by a later rollup job (ADR 0001 §5). Each rollup stores avg and max where meaningful.
- Range → resolution: Live/30 min → 1 s (downsample to ≤ 1 800 points), 1 h → 10 s (from memory), 6 h/24 h → 1 m, 7 d → 5 m, 30 d → 1 h.

### 10.5 API format
Columnar, uPlot‑native:
```json
{ "start": 1760412300, "end": 1760412600, "step": 1,
  "timestamps": [1760412300, 1760412301, ...],
  "series": { "open_connections": [2210, 2215, ...], "connections_per_second": [41, 18, ...],
              "requests_per_second": [8820, 8790, ...], "bandwidth.in": [4.1e6, ...], "bandwidth.out": [4.3e6, ...] } }
```
History: `GET /api/v1/projects/{id}/metrics?type=…` returns the project (`pr`) series; `&service_id=<S>` returns that service's backend (`be_<S>`) series. Live: `GET /api/v1/projects/{id}/metrics/stream` (SSE) pushes one compact point per second.

### 10.6 Optional exports
- `sys_stats` frontend on `127.0.0.1:8405`: HAProxy's built‑in Prometheus exporter at `/metrics` (render only if `haproxy -vv` shows PROMEX) and the native stats page at `/stats`. Because names are `fe_<id>`/`be_<id>`, loxy's own `/metrics` exposes an info series mapping service ids to project ids, names and labels.
- loxy's own `/metrics` (controller health: reload durations, apply errors, poll latency, ACME results).

---

## 11. Certificates and ACME

### 11.1 Storage
Certificates belong to the single tenancy project and are reusable across all load‑balancer projects, services and domains. Private keys are encrypted at rest (AES‑256‑GCM, key from `LOXY_SECRET_KEY`). The rendered revision necessarily contains plaintext PEMs (HAProxy reads them); the config volume is mode 0750, files 0640, group `loxy` (§16).

### 11.2 Managed certificates with lego
- One ACME account per directory URL (`acme_accounts`), created lazily with the configured email (and EAB when required by the CA).
- **HTTP‑01**: implement lego's `challenge.Provider` (`Present(ctx, domain, token, keyAuth)` / `CleanUp(...)`). `Present` pushes `add map virt@acme_http01 <token> <keyAuth>` to **every node** that serves the domain, waits for acknowledgement, then returns; `CleanUp` deletes the entry. HAProxy answers the challenge itself with the `http-request return … map(virt@acme_http01)` rule in `sys_http` (§7.4) — no extra hop, no port juggling. The virtual map only exists once a loaded config references it, so issuance waits until `sys_http` is live.
- **DNS‑01**: a lego `challenge.Provider` implemented on top of the libdns provider of a connected DNS provider account (§8.12), so DNS credentials are stored once (encrypted) and shared by record management and ACME; required for wildcard domains.
- Key type default ECDSA P‑256; RSA optional.
- Issuance runs as an async action; failures record the lego error and retry with backoff (1 m, 5 m, 30 m, 2 h, then every 6 h) without blocking the service (placeholder cert keeps serving) or the domain (stays on http).
- **Renewal** scheduler: honour ACME Renewal Information (ARI) when the CA offers it; otherwise renew at 2/3 of the lifetime (≈ 30 days before expiry for 90‑day certs). Jitter start times. On success: new revision with the new PEM + runtime `set ssl cert` / `commit ssl cert` (no reload).
- Use Let's Encrypt **staging** automatically in dev and CI (Pebble in tests).
- HAProxy's native ACME (`acme` section, 3.2+) MAY replace lego later once it's no longer experimental; keep the `CertIssuer` interface abstract so that's a swap, not a rewrite.

### 11.3 Uploaded certificates
Same runtime hot‑swap path when replaced. Expiry warnings at 21/7/1 days.

---

## 12. REST API

The contract is `api/openapi.yaml` (OpenAPI 3.0.3); this section summarises it. When they disagree, fix one of them in the same PR.

### 12.1 Conventions
- Base path `/api/v1`, JSON, `snake_case`, plural resource names (`projects`, `services`, `targets`, `hosts`, `certificates`, `domains`, `actions`, `metrics`).
- Auth: session cookie (UI) or `Authorization: Bearer loxy_<prefix>_<secret>` (API tokens).
- Pagination: `?page=&per_page=` (max 100) with `meta.pagination`. Filtering: `label_selector=`, `name=`. Sorting: `sort=name:asc`.
- Optimistic concurrency: every mutable resource has `version`; PUT/PATCH honour `If-Match` (ADR 0001 §6); mismatch → `409 conflict`.
- Errors: `{ "error": { "code": "invalid_input", "message": "...", "details": { "fields": [{ "name": "service.listen_port", "messages": ["port 20001 is used by project shop (#1)"] }] } } }`. Codes: `invalid_input`, `conflict` (incl. port/domain in use and in‑use deletes), `protected`, `not_found`, `forbidden`, `unauthorized`, `rate_limited`, `apply_failed`.
- Idempotency: POSTs accept `Idempotency-Key`.

### 12.2 Endpoints

| Method & path | Purpose |
|---|---|
| `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, `POST /auth/password`, `POST /auth/totp/{setup,enable,disable}` | Session auth, password change, 2FA |
| `GET /overview` | Dashboard counts: projects, targets, certificates, node, traffic |
| `GET/POST /projects`, `GET/PATCH/DELETE /projects/{id}` | Project CRUD. POST takes `name` plus optional `targets[]` and `service` (default: http, automatic port, destination 80) in one transaction; PATCH: name, description, algorithm, labels, maintenance |
| `GET /projects/{id}/config` | Rendered HAProxy sections of this project (read‑only, text) |
| `GET /projects/{id}/metrics?type=…&start=&end=&step=&service_id=`, `GET /projects/{id}/metrics/stream` | History (§10.5), SSE live points |
| `GET /projects/{id}/logs/stream` | SSE live logs (when enabled) |
| `GET/POST /projects/{id}/services`, `PUT/DELETE /projects/{id}/services/{sid}` | Services (omit `listen_port` for automatic allocation; DELETE → `409` when it is the last http/https service with domains) |
| `GET/POST /projects/{id}/targets`, `PATCH/DELETE /projects/{id}/targets/{tid}` | Targets (POST takes a list; PATCH: weight, admin_state, backup; DELETE is graceful) with resolved slots and per‑service checks |
| `GET/POST /hosts`, `PATCH/DELETE /hosts/{id}` | Inventory |
| `GET /label_selector/preview?selector=` | Hosts matched by a selector |
| `GET/POST /certificates`, `GET/PATCH/DELETE /certificates/{id}`, `POST /certificates/{id}/actions/renew` | Certificates |
| `GET/POST /domains`, `GET/PATCH/DELETE /domains/{id}`, `POST /domains/{id}/actions/verify`, `POST /domains/{id}/actions/issue_certificate` | Domains (`project_id`, `service_id`, `https`; filter `?project_id=`; §8.11–8.12) |
| `GET/POST /dns_providers`, `DELETE /dns_providers/{id}`, `GET /dns_providers/{id}/zones`, `GET/POST/DELETE /dns_providers/{id}/zones/{zone}/records` | DNS providers (incl. available kinds) and zone editor (§8.12; records Phase 2) |
| `GET /nodes`, `PATCH /nodes/{id}`, `POST /nodes/{id}/ips`, `DELETE /nodes/{id}/ips/{ipid}` | Node info and node addresses |
| `GET /nodes/{id}/revisions`, `GET /nodes/{id}/revisions/{rev}/diff?against=`, `POST /nodes/{id}/revisions/{rev}/actions/rollback` | Config revisions |
| `POST /apply/retry` | Re‑run the reconciler after an apply error |
| `GET /actions`, `GET /actions/{id}` | Async action status |
| `GET /events` (SSE), `GET /events/history` | Event stream & history (filter `?project_id=`) |
| `GET /audit` | Audit log |
| `GET/POST/DELETE /users`, `/api_tokens`, `/notification_channels` (+ `POST …/{id}/actions/test`), `/alert_rules`, `GET/PATCH /settings` | Admin |
| `GET /healthz`, `GET /readyz`, `GET /metrics` | Ops (no auth on healthz; readyz checks DB + HAProxy socket) |

### 12.3 Example resources
```json
{
  "project": {
    "id": 1, "name": "shop", "description": "web shop", "version": 9,
    "algorithm": "roundrobin", "maintenance": false, "labels": { "env": "prod" },
    "status": "ready", "last_error": null, "created_at": 1760412300000,
    "endpoints": [
      { "kind": "port", "url": "http://203.0.113.10:20001", "service_id": 1 },
      { "kind": "port", "url": "https://203.0.113.10:20002", "service_id": 2 },
      { "kind": "domain", "url": "https://shop.example.com", "service_id": 1, "domain_id": 4 }
    ],
    "counts": { "targets": 3, "services": 2, "domains": 2 },
    "health": { "status": "healthy", "healthy": 4, "total": 4, "messages": [] },
    "stats": { "open_connections": 2210, "requests_per_second": 8820, "bytes_out_per_second": 4.3e6 }
  }
}
```
```json
{
  "service": {
    "id": 1, "project_id": 1, "protocol": "http", "listen_port": 20001, "destination_port": 8080,
    "proxy_protocol": "none", "timeout_s": 60, "certificates": [],
    "sticky_sessions": false, "cookie_name": "LOXYSTICKY", "cookie_lifetime_s": 300,
    "health_check": { "protocol": "http", "port": null, "interval_s": 15, "timeout_s": 10, "retries": 3,
                      "http": { "domain": "shop.example.com", "path": "/health", "response": "OK", "status_codes": ["2??", "3??"], "tls_verify": false } },
    "health": { "status": "healthy", "healthy": 4, "total": 4, "messages": [] },
    "domains": ["shop.example.com", "beta.example.com"]
  }
}
```
```json
{
  "target": {
    "id": 102, "project_id": 1, "type": "label_selector", "label_selector": "role=web",
    "port": null, "use_private_ip": false, "weight": 100, "backup": false, "admin_state": "ready", "removing": false,
    "resolved": [
      { "slot_id": 102, "host_id": 7, "name": "web-1", "address": "10.0.0.12", "status": "healthy",
        "checks": [ { "service_id": 1, "status": "healthy", "check": "L7OK/200 in 3 ms" },
                    { "service_id": 2, "status": "healthy", "check": "L4OK in 1 ms" } ] }
    ]
  }
}
```
```json
{
  "domain": {
    "id": 4, "fqdn": "shop.example.com", "project_id": 1, "service_id": null, "https": true,
    "dns_provider_id": null, "auto_records": false, "status": "ok",
    "required_records": [ { "type": "A", "name": "shop.example.com", "value": "203.0.113.10", "ttl": 300 } ],
    "observed": { "a": ["203.0.113.10"], "aaaa": [] }, "certificate_id": 21,
    "url": "https://shop.example.com"
  }
}
```

### 12.4 Actions
Long or multi‑step operations return `202` with `{ "action": { "id", "command", "status": "running|success|error", "progress": 0-100, "resources": [...], "error": null, "started", "finished" } }`. The UI follows actions over the `/events` SSE stream (no polling).

### 12.5 SSE event kinds
`project.updated`, `apply.started|succeeded|failed`, `target.health.changed` (with `project_id`), `certificate.updated`, `domain.status.changed`, `domain.sync.failed`, `action.updated`, `node.status.changed`, `metrics.tick` (only on metric streams). Reconnect with `Last-Event-ID`; server keeps a 2‑minute replay buffer.

### 12.6 Codegen
`make gen` runs `oapi-codegen` (Go server interfaces + models) and `openapi-typescript` (web types). CI fails if generated files are stale.

---

## 13. Frontend implementation

- SvelteKit SPA (`adapter-static`, fallback `index.html`, `ssr = false`), served by loxy from `go:embed` with precompressed `.br`/`.gz` assets, `Cache-Control: public, max-age=31536000, immutable` for hashed files and `no-cache` for `index.html`.
- Routes mirror the tabs so every tab is linkable (`/projects/1/targets`, `/projects/1/domains`).
- Data: one session‑scoped snapshot store fed by REST on navigation and kept fresh by the single `/events` SSE connection; mutations are optimistic where safe (labels, names) and action‑driven otherwise.
- Charts: `Chart.svelte` wraps uPlot, dynamically imported only on the Graphs tab (sparklines on the Dashboard and lists use a 1 KB SVG polyline component, not uPlot).
- Components: `StatBar`, `StatCard`, `Donut`, `HealthBadge`, `Tabs`, `Table` (virtualised above 200 rows), `Modal`, `Dropdown`, `Toast`, `LabelChips`, `LabelSelectorInput`, `PortField` (automatic / custom), `EndpointList`, `TargetAdder`, `ServiceForm`, `DomainForm`, `RequiredRecords`, `CertPicker`, `HostPicker`, `CopyText`, `ConfirmTyped`, `CodeView` (read‑only config/diff viewer, no heavy editor).
- Wording: the UI says project, target, service, domain, port — never frontend/backend/server (except in the read‑only Config view, which shows HAProxy text).
- Design tokens in `tokens.css`; dark theme default, light theme via `prefers-color-scheme` + toggle; accent colour configurable in Settings:
  ```css
  :root { --bg:#141414; --surface:#1f1f1f; --surface-2:#272727; --border:#343434;
          --text:#f1f1f1; --muted:#9b9b9b; --accent:#e5484d; --ok:#30a46c; --warn:#f5a524;
          --err:#e5484d; --radius:10px; --font: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  ```
- Accessibility: keyboard navigation for tabs/menus/modals, focus rings, ARIA roles, colour + icon (never colour only) for health.
- Performance rules: no CSS framework runtime, no moment/lodash, no icon fonts; route‑level code splitting; skeletons instead of spinners; bundle analysis in CI with a hard budget (§1.4).
- Feasible on phone widths (cards stack; tables scroll horizontally inside their container).
- **Design process:** frontend work uses the project‑level Claude Code skills in `.claude/skills/`: `frontend-design`, `ui-ux-pro-max` and `emil-design-eng` set the design direction; `web-design-guidelines` is followed throughout; every screen gets a `critique` → `polish` → `quieter` → `harden` pass (from `impeccable`) before it is called done.

---

## 14. Security

### 14.1 Authentication & sessions
- First start: create the owner account with a random password printed once to the logs (`loxy admin reset-password` regenerates it).
- Passwords: argon2id (memory 64 MB, iterations 3, parallelism 2), minimum length 12.
- Sessions: 256‑bit random id, stored hashed; cookie `__Host-loxy_session; Secure; HttpOnly; SameSite=Strict; Path=/`; idle timeout 12 h, absolute 7 d; rotate on login.
- Optional TOTP 2FA, enforceable for all users.
- Login rate limit: 5 attempts / 15 min per IP + per account, exponential backoff.

### 14.2 Authorization
Roles: **owner** (everything incl. users, secrets), **admin** (all resources, settings except owner transfer), **operator** (projects, services, targets, hosts, certificates, domains; no users/settings), **viewer** (read‑only). API tokens carry a role and optional expiry; shown once at creation; stored as SHA‑256 hash with a visible prefix.

### 14.3 Web hardening
CSRF: SameSite=Strict cookies **and** a required `X-LOXY-CSRF` header token on mutating requests. Headers: strict CSP (`default-src 'self'`), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `Frame-Options: DENY`, HSTS on the panel. Request body limit 1 MB (5 MB for cert upload).

### 14.4 Input validation (config‑injection defence)
User values end up inside `haproxy.cfg`. **Never** interpolate raw text. Validate with strict allow‑lists, then render through a typed template layer that quotes per HAProxy rules (single quotes; reject values containing quotes).

| Field | Rule |
|---|---|
| Resource display names | 1–64 chars, any printable; **never rendered** into config |
| Label keys | `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?[a-zA-Z0-9]([-a-zA-Z0-9_.]{0,61}[a-zA-Z0-9])?$` |
| Label values | `^([a-zA-Z0-9]([-a-zA-Z0-9_.]{0,61}[a-zA-Z0-9])?)?$` |
| Domains (domain FQDNs, health check Host, cert SANs) | RFC 1123 hostname, lowercase, ≤ 253; `*.` prefix only for domains/certificates using DNS‑01 |
| Health check path | `^/[A-Za-z0-9._~!&()*+,;=:@%/?-]{0,254}$` (no spaces, quotes, `#`, `$`, `\`) |
| Health check response string | printable ASCII except `'"\#$`, 1–128 chars |
| Cookie name | `^[A-Za-z0-9_-]{1,64}$` |
| Ports | integers 1–65535; listen ports additionally not reserved (§8) |
| IPs / CIDRs, target `ip[:port]` | parsed with `net/netip` (`netip.ParseAddrPort` for the port form, IPv6 as `[addr]:port`); zone ids rejected; targets reject loopback (unless dev), link‑local, multicast, unspecified |
| Status code items | `^[1-5][0-9?]{2}$`, ≤ 20 items |
| Label selector | parsed by our own small parser into an AST; never passed to HAProxy |

Tests: a Go fuzz target generates random valid models (projects, services, targets, domains) and asserts the rendered config (a) passes `haproxy -c`, (b) contains no line not produced by a known template block, (c) never contains a newline inside a value.

### 14.5 Secrets at rest
`LOXY_SECRET_KEY` (32 random bytes, base64) in `.env` (mode 0600). Encrypts private keys, DNS credentials, TOTP secrets, notification credentials, ACME account keys. Key rotation command: `loxy admin rotate-secret`.

### 14.6 Panel exposure
loxy listens on `127.0.0.1:9090` only. Public access goes through the `sys_panel` HAProxy frontend (TLS, HTTP/2, optional IP allowlist, optional ACME cert for `LOXY_PANEL_DOMAIN`). Break‑glass access: SSH tunnel to `127.0.0.1:9090` always works even if the HAProxy config were broken.

### 14.7 Container & host hardening
- loxy runs as uid/gid 1500 (non‑root), read‑only root FS, writable volumes only.
- HAProxy container starts as root only to bind low ports and then drops to `user haproxy` (set in `global`); `cap_drop: [ALL]`, `cap_add: [NET_BIND_SERVICE, SETUID, SETGID, KILL, CHOWN]` (+ `SYS_CHROOT` if `chroot auto` is enabled). The master must be able to signal workers running as another uid, hence `KILL`. **VERIFY** the minimal capability set in the integration suite; if startup fails with EPERM, add the missing capability and record it.
- Sockets `mode 660 gid 1500`; config files group‑readable by gid 1500, and the HAProxy container gets `group_add: ["1500"]` so the (capability‑dropped) root master can read them.

---

## 15. Multi‑node and HA (Phase 3)

### 15.1 Panel ↔ agent
- `loxy agent` runs next to HAProxy on each HAProxy node (same compose as all‑in‑one minus the panel). It dials **out** to the panel over HTTPS WebSocket, so nodes need no inbound management port.
- Enrollment: panel creates a node and shows a one‑time join token; `loxy agent join --panel https://panel.example.com --token …` stores a long‑lived node credential and pins the panel's TLS certificate fingerprint.
- Projects are placed on a node or an HA group; their services, targets and domains are rendered on those nodes. Listen ports stay unique installation‑wide (simple, and lets a project move between nodes).
- Messages: panel → agent `DesiredState{version, haproxy config inputs, certs, acme_tokens}`; agent → panel `Applied{version, result, output}`, `StatsBatch` (1 s samples, sent every 1 s), `HealthDelta`, `NodeInfo`.
- The agent persists the last applied desired state locally and keeps running autonomously if the panel is down (HAProxy keeps serving; ACME HTTP‑01 tokens pushed before disconnect stay valid).

### 15.2 HA pairs (active/passive, generic)
- Both nodes of an HA group receive identical desired state for the group's projects; both may bind the group's **virtual IPs** at all times (`net.ipv4.ip_nonlocal_bind=1` and `net.ipv6.ip_nonlocal_bind=1` on the host), so failover needs no reload. Domains of projects on an HA group require the virtual IPs as their DNS records.
- Leader election and address movement: **keepalived** VRRP (in **unicast** mode between the nodes' private addresses by default; multicast optional) owns the virtual IP(s) and moves them to the backup node; a `track_script` checks HAProxy (`/healthz` of the agent + a TCP probe of a service port). `notify_*` scripts report state changes to loxy (`loxy ha notify <state>`), which records events. No provider API is called; environments that need provider‑specific address routing can hook their own script into `notify_master`.
- Loxy renders the keepalived config from the HA group (virtual IPs, peer addresses, priorities, auth) into `deploy/keepalived/` templates; keepalived runs as its own container (`network_mode: host`, `NET_ADMIN`) — an ADR is required before adding it.
- Stick tables (sticky/rate‑limit state) are synced with an HAProxy `peers` section between the two nodes.
- UI: HA group page on System shows active/backup, virtual IPs, last failover, "Fail over now" button (lowers the local VRRP priority temporarily).

---

## 16. Deployment

### 16.1 Host requirements
Linux x86_64 or arm64, kernel ≥ 5.4, Docker Engine ≥ 24 with Compose v2, 1 vCPU / 1 GB RAM minimum (HAProxy scales with cores and memory independently of loxy).

`deploy/sysctl/99-loxy.conf`:
```
net.ipv4.ip_nonlocal_bind = 1
net.ipv6.ip_nonlocal_bind = 1
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 65535
net.core.netdev_max_backlog = 65535
net.ipv4.ip_local_port_range = 1024 65000
net.ipv4.tcp_tw_reuse = 1
fs.file-max = 2097152
fs.nr_open = 2097152
```
(If `nf_conntrack` is loaded, raise `net.netfilter.nf_conntrack_max` accordingly or exclude service ports from tracking.)

Note: the ephemeral port range (`1024 65000`) overlaps the automatic service ports (20000+). A listening socket always wins over later outgoing connections, but an outgoing connection that already holds a port blocks a new listener on it until it closes; the API reports the bind error from `haproxy -c`/reload as `apply_failed` with the HAProxy text. Operators who want strict separation MAY reserve the automatic range with `net.ipv4.ip_local_reserved_ports = 20000-29999`.

### 16.2 Dockerfile (`deploy/docker/Dockerfile`)
```dockerfile
# syntax=docker/dockerfile:1
ARG HAPROXY_VERSION=3.4.6
ARG GO_VERSION=1            # current stable Go 1.x
ARG NODE_VERSION=24

FROM node:${NODE_VERSION}-alpine AS web
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY web/ ./
COPY api/openapi.yaml /src/api/openapi.yaml
RUN pnpm run build          # emits build/ with .br and .gz siblings

FROM golang:${GO_VERSION}-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/build ./internal/webui/dist
ARG LOXY_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${LOXY_VERSION}" -o /out/loxy ./cmd/loxy

FROM haproxy:${HAPROXY_VERSION}-alpine
USER root
RUN addgroup -g 1500 loxy && adduser -D -H -u 1500 -G loxy loxy
COPY --from=go /out/loxy /usr/local/bin/loxy
USER 1500:1500
ENTRYPOINT ["/usr/local/bin/loxy"]
CMD ["all-in-one"]
```
Image tags: `ghcr.io/2shiraz/loxy:<loxy-version>-haproxy<haproxy-version>`. At start loxy compares its bundled `haproxy -v` with the running HAProxy's `show info` version and shows a warning banner on mismatch.

### 16.3 Compose (`deploy/compose/compose.yml`)
```yaml
name: loxy
services:
  loxy:
    image: ghcr.io/2shiraz/loxy:${LOXY_VERSION}-haproxy${HAPROXY_VERSION}
    network_mode: host
    restart: unless-stopped
    read_only: true
    env_file: .env
    volumes:
      - ./data/haproxy-conf:/usr/local/etc/haproxy      # revisions + live symlink (rw for loxy)
      - ./data/haproxy-state:/var/lib/haproxy           # server-state file
      - ./run/haproxy:/run/haproxy                      # sockets
      - ./data/loxy:/var/lib/loxy                         # loxy.db, backups
    tmpfs: [/tmp]
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:9090/healthz"]
      interval: 5s
      timeout: 2s
      retries: 12

  haproxy:
    image: haproxy:${HAPROXY_VERSION}-alpine
    network_mode: host
    restart: unless-stopped
    user: "0:0"
    group_add: ["1500"]
    entrypoint: ["haproxy", "-W", "-db",
                 "-S", "/run/haproxy/master.sock,mode,660,gid,1500",
                 "-f", "/usr/local/etc/haproxy/live/haproxy.cfg"]
    cap_drop: [ALL]
    cap_add: [NET_BIND_SERVICE, SETUID, SETGID, KILL, CHOWN]
    ulimits:
      nofile: { soft: 1048576, hard: 1048576 }
    volumes:
      - ./data/haproxy-conf:/usr/local/etc/haproxy:ro
      - ./data/haproxy-state:/var/lib/haproxy
      - ./run/haproxy:/run/haproxy
    depends_on:
      loxy: { condition: service_healthy }
    logging:
      driver: local
      options: { max-size: "10m", max-file: "3" }
```

### 16.4 Configuration (`.env`)

| Variable | Default | Meaning |
|---|---|---|
| `HAPROXY_VERSION` | `3.4.6` | Pinned HAProxy image version (same for both containers) |
| `LOXY_VERSION` | — | loxy release |
| `LOXY_SECRET_KEY` | generated by installer | 32 bytes base64, encrypts secrets at rest |
| `LOXY_LISTEN` | `127.0.0.1:9090` | Internal HTTP listener (its port is reserved, §8) |
| `LOXY_PANEL_PORT` | `8443` | Port of the `sys_panel` frontend (must not be 80/443) |
| `LOXY_PANEL_DOMAIN` | empty | If set, managed cert for the panel |
| `LOXY_PANEL_ALLOW` | empty | Comma‑separated CIDRs allowed to reach the panel |
| `LOXY_ACME_EMAIL` | empty | ACME account contact |
| `LOXY_ACME_DIRECTORY` | Let's Encrypt production | Directory URL |
| `LOXY_DATA_DIR` | `/var/lib/loxy` | DB, backups |
| `LOXY_HAPROXY_CONF_DIR` | `/usr/local/etc/haproxy` | Revision tree |
| `LOXY_HAPROXY_RUN_DIR` | `/run/haproxy` | Sockets |
| `LOXY_IPV6` | `auto` | IPv6 listeners for services and domains: `auto` (probe), `on`, `off` (ADR 0004 §9) |
| `LOXY_STATS_INTERVAL` | `1s` | Poll interval |
| `LOXY_LOG_LEVEL` | `info` | |

### 16.5 Installer (`deploy/install.sh`)
1. Check root, Docker ≥ 24, Compose v2, free ports (8443; 80/443 if domains will be used).
2. Write `/etc/sysctl.d/99-loxy.conf`, `sysctl --system`.
3. Create `/opt/loxy/{data/haproxy-conf,data/haproxy-state,data/loxy,run/haproxy}`; `chown -R 1500:1500`; `chmod 0750` on data dirs and `chmod 0770` on `run/haproxy` (the capability‑dropped HAProxy master creates its sockets there through group 1500).
4. Write `compose.yml` and `.env` (generate `LOXY_SECRET_KEY`, chmod 0600).
5. `docker compose pull && docker compose up -d`; wait for healthy.
6. Print panel URL (`https://<host>:8443`) and the one‑time owner password (`docker compose logs loxy | grep 'initial password'`), remind to restrict access (allowlist/firewall).

### 16.6 Operations
- **Upgrade loxy:** `docker compose pull loxy && docker compose up -d loxy` — HAProxy keeps serving, no traffic impact. DB migrations run at start after an automatic pre‑migration backup. Upgrading a database from the frontend/backend version runs `0002_projects.sql` (§6): frontend/backend definitions are dropped, so recreate them as projects; domains are kept detached and can be attached to the new projects.
- **Upgrade HAProxy:** bump `HAPROXY_VERSION` (and the matching loxy image tag) → `docker compose up -d`. Replacing the HAProxy container restarts it (brief interruption) — schedule it, or in an HA pair fail over first, upgrade passive, fail back.
- **Backup:** `loxy backup` (SQLite online backup via `VACUUM INTO` + current live revision tarball), nightly by default, keep 14; optional upload to S3‑compatible storage. **Restore:** `loxy restore <file>` then the reconciler re‑applies.
- **Break‑glass:** `docker compose exec loxy loxy admin reset-password`; `loxy render --dry-run` prints the config that would be applied; `loxy admin rollback --revision N`.
- **Firewall:** open the project ports you use, plus 80/443 when using domains; restrict 8443 to admin IPs; never expose 9090, 8405 or 5514.

---

## 17. Development workflow

- `make dev` → `docker compose -f deploy/compose/compose.dev.yml up` with: HAProxy (same image), three `traefik/whoami` targets on different ports (one can be toggled unhealthy via an env flag), **Pebble** (Let's Encrypt's ACME test server) + its challenge DNS, and loxy in dev mode with live reload; Vite dev server proxies `/api` to loxy.
- `make gen` (OpenAPI → Go + TS, sqlc), `make lint` (golangci‑lint, `svelte-check`, eslint, prettier), `make test`, `make test-integration`, `make e2e`, `make load`, `make build`, `make image`.
- `make seed` creates demo projects: "shop" (http service on an automatic port + an https service, 3 targets of all three types, two domains), "db" (tcp service), 3 hosts, an uploaded cert, a managed cert against Pebble, and synthetic traffic (`oha` loop) so graphs have data.
- Conventional commits; CI on every PR: gen‑check, lint, unit, integration (real HAProxy via testcontainers), e2e (headless), bundle‑size check, image build.

---

## 18. Testing strategy

| Layer | What |
|---|---|
| Unit | Validation rules (listen‑port uniqueness across projects, reserved ports, automatic port allocation incl. gaps and concurrent creates, domain uniqueness, default‑service resolution, refusal to remove the last http/https service with domains); status‑code conversion; label‑selector parser; target `ip[:port]` parsing; Host‑condition rendering (exact before wildcard); metric delta/reset logic and project = Σ services; health aggregation (per target per service); reconciler classification table (§7.6) |
| Migration | `0002_projects.sql` on a fixture database of the frontend/backend version: tenancy `projects`, hosts, certificates, users, settings, DNS providers survive; domains are copied detached; events keep their rows; frontend/backend tables are gone and `metrics_1m` is empty; a database with a `load_balancers` table is refused with the documented error |
| Renderer golden tests | Fixture models → expected `haproxy.cfg` (byte‑exact): the §7.4 reference, a project without targets, maintenance (http in `fe_` + `be_` only with domains, tcp in `fe_`), IPv6 off, `sys_http` only (no certificate issued yet), exact vs. wildcard domains of two projects, HTTP‑01 cert without domains; every golden passes `haproxy -c` in CI |
| Fuzz | Random valid models → config passes `haproxy -c`, no injected lines (§14.4) |
| Integration (real HAProxy 3.4) | Runtime client matchers for every command; reload success and failure paths (`Success=0` keeps old workers, symlink restored); server‑state survives reload; dynamic target add/remove/drain across two services; two projects with domains on shared :80/:443 route by Host, unknown Host → 404; 301 only after issuance; maintenance via map (http 503 on port and domain, tcp reject); cert hot‑swap; ACME HTTP‑01 + DNS‑01 against Pebble; metrics deltas across reloads; **all VERIFY items**: `-S` socket options, double `http-check expect` semantics, `@system-ca`, `del ssl cert`, minimal capability set, `http-request return` with `opt@` map in a backend, `chroot auto`, prompt mode, `shm-stats-file` (if enabled) |
| E2E (Playwright) | Login, create project with the wizard (automatic port), add targets of all three types, reach the project on its port, add https and tcp services, port conflict error, toggle sticky/PROXY protocol, drain/maint/weight a target, maintenance mode, attach a domain → verification → certificate → 301, detach domain, delete project, certificate upload/managed flow, graphs render (project and per service), rollback |
| Load / zero‑downtime | `oha` at 2 000 rps against a service port and a domain while (a) 100 reloads, (b) 50 target add/removes, (c) cert renewals — **0 non‑2xx/connection errors** allowed |
| Resource | loxy RSS and CPU under the §1.4 dataset (50 projects / 200 services / 1 000 targets); UI bundle size budget |

---

## 19. Roadmap and Definitions of Done

**Phase 0 — Foundations** *(done)*
Repo skeleton, Makefile, CI, OpenAPI skeleton + codegen, SQLite + migrations, auth (owner login, sessions), runtime client + parsers, renderer + golden tests, revision FS + validator, bootstrap config, reload procedure.
*DoD:* `docker compose up` on a clean VM brings up HAProxy with the panel reachable at :8443; `loxy render --dry-run` works; reload path integration tests pass.

**Phase 1 — Load‑balancer MVP on a single node (projects model, ADR 0006)**
Migration `0002_projects.sql`; projects (algorithm, maintenance, labels) with the create wizard and automatic ports; services (http/https/tcp, listen → destination, health checks, sticky, PROXY protocol, timeout, certificates); targets (IP, host, label selector) with runtime apply, weight/backup/drain/maint and graceful removal; domains on shared `sys_http`/`sys_https` with automatic HTTPS (HTTP‑01 and DNS‑01), required records and live verification; hosts inventory; certificates (upload + managed); DNS provider accounts with encrypted credentials; node addresses; Dashboard, Projects list, project Overview/Graphs/Targets/Services/Domains/Settings (live + history graphs), global Domains page; Activity (events + audit); users/roles.
*DoD:* every acceptance criterion in §8 for these screens passes in E2E; zero‑downtime load tests pass; §1.4 budgets met; migration test green.

**Phase 2 — Operations polish**
Config revisions UI (list, diff, rollback, pin), notifications (webhook, email, Telegram, Slack) and alert rules with project label filters, API tokens, live logs (sampled) on the project Logs tab, Prometheus endpoints, backups (+ S3), 2FA, `loxy` CLI client for the API, **DNS part 2** (§8.12): auto‑managed A/AAAA records and the zone editor.
*DoD:* alert on target down arrives within 5 s; rollback restores previous behaviour verified by integration test; nightly backup + restore round‑trip test.

**Phase 3 — Multi‑node and generic HA**
`loxy panel` / `loxy agent`, enrollment, node pages, HA groups with keepalived VRRP and a virtual IP (no provider API), `peers` sync of stick tables.
*DoD:* killing the active node moves the virtual IP and traffic to the passive node automatically; the panel going down doesn't affect traffic; agent reconnects and reconciles.

**Phase 4 — Advanced L7 (optional)**
Path routing inside a project (path prefix → service), rate limiting per client IP (stick tables), IP allow/deny lists per project (runtime ACLs), request/response header rules, HSTS for domains, response compression (3.4 `filter comp-res`), custom error pages, HTTP/3 (QUIC) when the image reports QUIC support, TLS re‑encryption to targets, OIDC SSO for the panel.

---

## 20. Backlog of useful extras

- **Config import** from an existing `haproxy.cfg`: map frontend/backend pairs onto projects and services, show what can't be imported (kept out, listed), create the objects in one transaction, then let Loxy own the config.
- **Maintenance page** per project with custom text (instant, runtime map).
- **Graceful drain** with configurable timeout, and **slow start** (`slowstart`) for targets returning to service.
- **Live logs**: HAProxy logs to `127.0.0.1:5514` UDP → loxy ring buffer per project (by `be_<S>` name), sampling (e.g. 1:10), off by default (privacy and performance first).
- **Config preview & diff** before applying risky changes; "Download HAProxy config".
- **Per‑target graphs** and response‑time / status‑code breakdowns.
- **Bandwidth limits** per service using HAProxy's `bwlim-in`/`bwlim-out` filters.
- **Duplicate project**, **Export/Import JSON**, Terraform provider later (the API is resource‑shaped, which makes this easy).
- **Webhooks** for every event kind.
- **Rate limiting & IP lists** via stick tables and runtime ACLs (Phase 4).
- **HTTP/3** advertisement (`alt-svc`) when QUIC is available.
- **Runtime backends** (3.4 `add backend` / `publish backend`) to make some service changes reload‑free.
- **Own authoritative DNS server** — explicitly *not* planned (non‑goal); revisit only if operators need GeoDNS/health‑based DNS failover. Today Loxy manages records at external providers (§8.12).
- **More DNS providers** via libdns (deSEC, Route 53, DigitalOcean, OVH, …) — one registry entry each.
- **Health‑check templates** using the 3.4 `healthcheck` section (reusable checks shared by services).

---

## 21. Open questions for the owner (ask before Phase 1 ends; defaults in brackets)

1. Accent colour and logo [neutral red, simple wordmark "Loxy"]. (Name is decided: **Loxy**.)
2. Will people other than you log in? [single owner + team; one tenancy project (`projects` id 1) — the load‑balancer projects are `balancers` in the DB; per‑project permissions are backlog]
3. Is HA in scope for the first production rollout? [no — Phase 3, generic VRRP]
4. DNS providers for DNS‑01 and record management [the libdns providers already in the `internal/dns` registry; more are one registry entry each, §8.12].
5. Automatic port range [lowest free port in 20000–29999; explicit ports may be anywhere else that is free].
6. Should a domain default to HTTPS? [yes — `https` on, :80 redirects once the certificate is issued].

---

## 22. References

- HAProxy releases and LTS schedule — https://www.haproxy.org/
- Announcing HAProxy 3.4 (dynamic backends, compression syntax, ACME additions, `healthcheck` section, `stats calculate-max-counters`) — https://www.haproxy.com/blog/announcing-haproxy-3-4
- HAProxy 3.4 configuration manual — https://docs.haproxy.org/3.4/configuration.html
- HAProxy management guide (master CLI, `reload`) — https://docs.haproxy.org/3.4/management.html
- Runtime API: `add server` (dynamic server rules) — https://www.haproxy.com/documentation/haproxy-runtime-api/reference/add-server/
- Runtime API: `set ssl cert` / `new ssl cert` / `add ssl crt-list` — https://www.haproxy.com/documentation/haproxy-runtime-api/reference/set-ssl-cert/
- HAProxy ACME tutorial (native ACME, experimental) — https://www.haproxy.com/documentation/haproxy-configuration-tutorials/security/ssl-tls/acme/
- HAProxy health checks tutorial — https://www.haproxy.com/documentation/haproxy-configuration-tutorials/reliability/health-checks/
- HAProxy 3.3.0 release notes (default `random` balance, name rules) — https://www.mail-archive.com/haproxy@formilux.org/msg46300.html
- Data Plane API 3.3 / 3.4 release notes — https://github.com/haproxytech/dataplaneapi/releases/tag/v3.3.0 · https://www.haproxy.com/documentation/haproxy-data-plane-api/release-notes/v3.4/
- Official Docker image — https://hub.docker.com/_/haproxy
- keepalived (VRRP) — https://keepalived.readthedocs.io/
- libdns (provider‑agnostic DNS record APIs for Go) — https://github.com/libdns/libdns
- lego ACME library (custom challenge providers, DNS providers) — https://go-acme.github.io/lego/
- Roxy‑WI (for contrast) — https://github.com/roxy-wi/roxy-wi
- ADR 0006 — projects model (`docs/adr/0006-projects.md`)
