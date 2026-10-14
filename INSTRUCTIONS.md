# Loxy — a self‑hosted management panel for HAProxy

**Build instructions for the implementing agent**

| | |
|---|---|
| Name | **Loxy** — repo `github.com/2shiraz/loxy`, Go module `github.com/2shiraz/loxy`, binary `loxy`, image `ghcr.io/2shiraz/loxy`, env prefix `LOXY_`, DB file `loxy.db` |
| Data plane | **HAProxy 3.4 LTS** (official Docker image `haproxy:3.4.x-alpine`, patch version pinned) |
| Control plane | Single Go binary with embedded web UI, SQLite, no other services |
| Purpose | Manage the **frontends, backends, servers, certificates and DNS** of HAProxy safely: every change is validated (`haproxy -c`), versioned as a revision, applied without dropping traffic (runtime API or hitless reload) and observable (health, graphs, logs, activity). A replacement for hand‑editing `haproxy.cfg` and for heavier HAProxy GUIs. |
| Spec date | 2026‑10‑14 (product direction: ADR 0005) |

---

## 0. How to use this document

1. Read the whole file before writing code. Sections 1–7 are **decisions and contracts**, 8–14 are **feature specs**, 15–19 are **deployment, testing and the phased roadmap**.
2. Keywords: **MUST** = required, **SHOULD** = strong default (deviate only with a written reason in `docs/adr/`), **MAY** = optional.
3. Build in the phases of §19. A phase is done only when its Definition of Done (DoD) is green in CI.
4. Items tagged **VERIFY** are HAProxy behaviours that must be proven by an automated integration test against the pinned HAProxy image *before* code relies on them. If the test disproves the statement, the observed behaviour wins: change the code, keep the test, and note it in `docs/haproxy-notes.md`.
5. Do not add runtime dependencies beyond §4 without an ADR. "Lightweight and fast" is a hard requirement, not a preference.
6. Never let a rendered HAProxy config that has not passed `haproxy -c` become the live config (§7.7).

---

## 1. Product summary

### 1.1 What we are building
A self‑hosted control panel + REST API for one or more Linux servers running HAProxy. The operator works with HAProxy's own concepts, presented safely:

- **Frontends** — where traffic enters: a mode (`http | tcp`), one or more **binds** (`address:port`, address `*` = all interfaces), optional **TLS termination** with certificates, an optional HTTP→HTTPS redirect listener, a **default backend** and (http) **host/path routing rules** to other backends, timeouts, `maxconn`, forwarded headers, enable/maintenance switches and labels.
- **Backends** — where traffic goes: a mode, a load‑balancing **algorithm**, a default server port, **health checks**, sticky sessions, PROXY protocol, timeouts and labels. A backend can be used by many frontends.
- **Servers** of a backend — a raw **IP**, a **host** from the inventory, or a **label selector** that matches many hosts; weight, backup flag and admin state (ready / drain / maint), added and removed at runtime.
- **Hosts** (inventory of machines), **Certificates** (uploaded or managed via ACME), **Domains & DNS** (records at external DNS providers).
- The panel shows **health** per server and backend, **live and historical graphs** per frontend and backend (open connections, connections/s, requests/s, bandwidth, sessions, queue, response time, HTTP status classes, errors), live logs and an activity/audit trail.
- Every change is applied to HAProxy automatically, validated, versioned, and **without dropping traffic** (runtime API where possible, hitless reload otherwise).

### 1.2 Goals
1. **Cover the everyday HAProxy job without touching `haproxy.cfg`:** frontends, backends, servers, TLS, routing, health checks, certificates, DNS (§8), plus the extras in §20.
2. **Minimal install:** `docker compose up -d` with two containers (official HAProxy + `loxy`). No external database, queue, Prometheus or Redis required.
3. **Fast and light:** budgets in §1.4 are acceptance criteria.
4. **Safe:** invalid config can never reach the live path; every applied state is a numbered revision that can be diffed and rolled back.
5. **API‑first:** the UI uses only the public REST API, so everything is scriptable.

### 1.3 Non‑goals (v1)
Being a cloud load‑balancer *product* (no "create load balancer" objects, sizes/plans, rescaling, prices, billing or traffic quotas); managing NGINX/Apache; Kubernetes ingress; a WAF rule engine; anycast/multi‑region traffic steering; UDP load balancing; provider‑specific cloud integrations (inventory sync, floating‑IP APIs); **running an authoritative DNS server** (Loxy manages records at external DNS providers through their APIs, §8.9; it never answers DNS queries itself); exposing every HAProxy keyword (Loxy is opinionated; raw config import is a backlog item, §20).

### 1.4 Performance budgets (acceptance criteria)

| Item | Budget |
|---|---|
| Controller resident memory, idle, 50 frontends / 200 backends / 1 000 servers | ≤ 60 MB |
| Controller CPU, idle (1 s stats polling) | < 1 % of one core |
| API read p95 (served from in‑memory snapshot) | < 10 ms |
| Write that is applied via Runtime API (e.g. add server), request → active in HAProxy | < 200 ms |
| Write that needs a reload (validate + swap + reload), 50 frontends / 200 backends | < 1.5 s |
| UI initial load, all JS + CSS gzip | ≤ 150 KB (charts lazy‑loaded on the Graphs tabs) |
| Live graph latency (counter change → pixel) | ≤ 2 s |
| Failed client requests during 100 consecutive reloads under a 2 000 rps load test | **0** |
| Failed client requests while adding/removing 50 servers under load | **0** |

---

## 2. Concept mapping (Loxy → HAProxy)

| Loxy object | Table(s) | HAProxy construct |
|---|---|---|
| Frontend (name, mode, binds, TLS, options) | `frontends`, `frontend_binds`, `frontend_certificates` | `frontend fe_<id> from loxy_http|loxy_tcp` with one `bind` per address:port (IPv6 as `ipv6@addr:port`, `*` → `:port` / `ipv6@:::port`) |
| HTTP→HTTPS redirect (tls only) | `frontends.http_redirect` | Extra `frontend fe_<id>_rd` on the same addresses:80 with the ACME HTTP‑01 rule and `http-request redirect scheme https code 301` |
| TLS termination + certificates | `frontends.tls`, `frontend_certificates` | `bind … ssl crt-list fe<id>.lst alpn h2,http/1.1` (`crt-base` + relative entries, ADR 0004); certs hot‑swapped through the Runtime API |
| Default backend | `frontends.default_backend_id` | `default_backend be_<id>` |
| Routing rule (hosts, path prefix → backend; http only) | `routes` (ordered by `position`) | `use_backend be_<id> if { req.hdr(host),field(1,:),lower -m str|-m end … } { path_beg … }` in route order |
| Maintenance switch | `frontends.maintenance` | `opt@maint.map` keyed by frontend id: http → `http-request return status 503`, tcp → `tcp-request connection reject` |
| Enabled switch | `frontends.enabled` | Frontend not rendered when disabled |
| Backend (mode, algorithm, port, options) | `backends` | `backend be_<id> from loxy_http|loxy_tcp`, `balance` **always explicit** (§7.9) |
| Health check (protocol, port, interval, timeout, retries, domain, path, response, status codes, TLS verify) | columns on `backends` | `option httpchk`, `http-check send/expect`, `check port inter rise fall`, `timeout check`, `check-ssl` |
| Sticky sessions (cookie name, lifetime) | backend columns | `cookie <name> insert indirect nocache httponly [secure] maxlife <n>s` + `cookie t<T>[x<H>]` per server |
| PROXY protocol | backend column | `send-proxy`/`send-proxy-v2` **and** `check-send-proxy` on server lines |
| Server: IP / host / label selector | `backend_servers`, resolved into `server_slots` | `server t<T>` (IP) or `server t<T>x<H>` (host / selector match) with `id <slot id>`; added/removed at runtime as dynamic servers |
| Host (inventory) | `hosts` | Address source for host/selector servers only |
| Labels | JSON column on frontends, backends, hosts, certificates | Selectors, list filters and alert‑rule filters only; never rendered |
| Node addresses | `node_addresses` | Not rendered; bind‑address suggestions and DNS required records for `*` binds |
| Metrics | collector + rollup tables (scope frontend / backend) | Deltas of `show stat` counters |
| System | — | `sys_panel` (+ `sys_panel_be`), `sys_stats`, `sys_acme_fe<id>` (ACME HTTP‑01 :80 listener for a TLS frontend without redirect) |

The metric names of the API (`open_connections`, `connections_per_second`, `requests_per_second`, `bandwidth` with `in`/`out` series, plus backend `sessions`, `queue`, `response_time_ms`, `http_*xx`, `errors`) are stable contract (§10.5).

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
          Clients ⇄ bind IP:port ⇄ HAProxy (frontends → backends) ⇄ Servers
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
7. On any failure the previous good revision stays (or is put back) as `live`, the affected frontends/backends and System show an `apply_error` banner with the HAProxy output, and a notification fires.

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
| DNS providers | `github.com/libdns/libdns` interfaces + provider modules (the first ones are listed in §21 Q4; ADR 0003) | One small, uniform record API (`GetRecords`/`AppendRecords`/`SetRecords`/`DeleteRecords`, `ListZones`) for many providers; the same credentials drive ACME DNS‑01 through a lego `challenge.Provider` adapter (§8.9) |
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
│   │   ├── migrations/              # 0001_init.sql, ...
│   │   ├── queries/                 # sqlc input
│   │   └── db/                      # sqlc output (generated, committed)
│   ├── state/                       # immutable NodeState snapshots, label-selector resolution, server slots
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
│   ├── dns/                         # libdns provider registry, lego DNS-01 adapter, domain verifier, record sync (§8.9)
│   ├── api/                         # handler implementations, middleware (auth, CSRF, rate-limit, request-id)
│   ├── auth/                        # sessions, argon2id, API tokens, RBAC, TOTP
│   ├── events/                      # in-process pub/sub + SSE hub
│   ├── audit/
│   ├── notify/                      # webhook, email (SMTP), Telegram, Slack
│   ├── logrecv/                     # UDP syslog receiver + per-frontend ring buffers (live logs)
│   ├── agent/                       # node-side applier + NodeTransport (in-process now, network in Phase 3)
│   ├── cluster/                     # panel-side node registry, enrollment, transport server (Phase 3)
│   └── webui/                       # go:embed of web/build (precompressed .br/.gz)
├── web/                             # SvelteKit SPA (§13)
│   ├── src/lib/api/                 # generated types + thin client
│   ├── src/lib/components/          # StatBar, StatCard, Donut, Chart, Tabs, Table, Modal, Dropdown, Toast, LabelChips, PortField, ...
│   ├── src/lib/stores/              # session, sse, snapshot cache
│   ├── src/lib/styles/tokens.css
│   └── src/routes/                  # /login, /dashboard, /frontends/[id]/{overview,routing,graphs,logs,config}, /backends/[id]/{servers,settings,graphs}, /hosts, /certificates, /domains/{,providers,zones}, /activity, /system, /settings/*
├── deploy/
│   ├── docker/Dockerfile
│   ├── compose/compose.yml          # production all-in-one
│   ├── compose/compose.dev.yml      # dev: haproxy + whoami servers + pebble ACME + loxy
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
    ├── adr/                         # architecture decision records
    ├── haproxy-notes.md             # verified behaviours, version quirks
    └── operations.md                # backup, restore, upgrade, break-glass
```

---

## 6. Data model (SQLite)

Conventions: `INTEGER PRIMARY KEY` ids (shown in UI as `#42`), timestamps as unix milliseconds, JSON stored as `TEXT CHECK(json_valid(col))`, `ON DELETE` rules explicit, `PRAGMA foreign_keys=ON`, `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`. All resources belong to a `project` (one default project in v1; keeps the door open for multi‑tenant use).

| Table | Key columns |
|---|---|
| `projects` | id, name, created_at |
| `users` | id, email (unique), password_hash (argon2id), role (`owner|admin|operator|viewer`), totp_secret_enc, disabled, created_at, last_login_at |
| `project_members` | project_id, user_id, role |
| `sessions` | id_hash, user_id, created_at, expires_at, ip, user_agent |
| `api_tokens` | id, project_id, name, prefix, secret_hash, role, expires_at, last_used_at |
| `nodes` | id, name, mode (`local|remote`), status, haproxy_version, loxy_version, features JSON (`QUIC`, `PROMEX`, …), last_seen_at, desired_version, applied_version |
| `node_addresses` | id, node_id, address, family (4/6), label — the node's own addresses: bind‑address suggestions and DNS required records for frontends that bind `*` |
| `node_groups` | id, name, kind (`single|ha_pair`), vip_mode (`none|vrrp`), virtual_ips JSON — Phase 3 |
| `frontends` | id, project_id, name (unique per project), description, node_id / node_group_id, mode (`http|tcp`), tls, http_redirect, default_backend_id (nullable, `ON DELETE RESTRICT`), timeout_client_s, maxconn (0 = unlimited), forwarded_headers, enabled, maintenance, labels JSON, status (`active|applying|apply_error`), last_error, options JSON (extras, §20), created_at, updated_at, version |
| `frontend_binds` | frontend_id, address (IP or `*`), port — UNIQUE(frontend_id, address, port); cross‑frontend collisions are checked in the API (§8.3) |
| `frontend_certificates` | frontend_id, certificate_id, position |
| `routes` | id, frontend_id, position, hosts JSON (exact or `*.example.com`), path_prefix, backend_id (`ON DELETE RESTRICT`) |
| `backends` | id, project_id, name (unique per project), description, mode (`http|tcp`), algorithm (`roundrobin|leastconn|source|random`), port, sticky_enabled, sticky_cookie_name, sticky_cookie_lifetime_s, proxy_protocol (`none|v1|v2`), timeout_server_s, hc_protocol (`tcp|http|https`), hc_port, hc_interval_s, hc_timeout_s, hc_retries, hc_domain, hc_path, hc_response, hc_status_codes JSON, hc_tls_verify, labels JSON, options JSON, created_at, updated_at, version |
| `backend_servers` | id, backend_id, type (`ip|host|label_selector`), ip, host_id, label_selector, port (nullable override), use_private_ip, weight (1–256, default 100), backup, admin_state (`ready|drain|maint`), removing, created_at |
| `server_slots` | id (stable HAProxy server `id`), backend_id, backend_server_id, host_id NULL, address, port — one row per *resolved* server, so ids survive reloads |
| `hosts` (inventory) | id, project_id, name, ipv4, ipv6, private_ipv4, labels JSON, notes |
| `certificates` | id, project_id, name, type (`uploaded|managed`), domains JSON, key_type (`ecdsa256|rsa2048|rsa4096`), challenge (`http01|dns01`), dns_provider_id (→ `dns_providers`), cert_pem, chain_pem, key_pem_enc, fingerprint_sha256, not_before, not_after, status (`pending|issued|renewing|failed`), last_error, renew_after, labels JSON |
| `acme_accounts` | id, directory_url, email, key_pem_enc, registration JSON, eab_kid, eab_hmac_enc |
| `dns_providers` | id, project_id, name, kind (registry key, e.g. `cloudflare`), credentials_enc (AES‑256‑GCM JSON), status (`ok|error|unknown`), last_error, last_checked_at, created_at |
| `domains` | id, project_id, fqdn (unique per project, RFC 1123, lowercase, `*.` allowed only with a DNS provider), frontend_id (nullable, `ON DELETE SET NULL`), dns_provider_id (nullable), zone (detected apex, e.g. `example.com`), auto_records (bool, default false), ttl (default 300), status (`unknown|ok|wrong_ip|missing|error`), observed JSON (last A/AAAA answers), checked_at, certificate_id (nullable), managed_record_ids JSON, created_at, updated_at |
| `config_revisions` | id, node_id, revision (monotonic), desired_version, sha256, kind (`runtime|reload|rollback|bootstrap`), result (`ok|failed`), duration_ms, haproxy_output, created_by, created_at, pinned |
| `metrics_1m`, `metrics_5m`, `metrics_1h` | scope (`frontend|backend`), scope_id, ts, open_conn_avg, open_conn_max, cps, rps, bytes_in, bytes_out, sessions_avg, queue_max, resp_time_ms_avg, http_2xx, http_3xx, http_4xx, http_5xx, errors — PK(scope, scope_id, ts) |
| `events` | id, ts, project_id, frontend_id NULL, backend_id NULL, kind, severity, message, data JSON |
| `audit_log` | id, ts, actor_user_id, actor_token_id, ip, action, resource_type, resource_id, before JSON, after JSON |
| `notification_channels` | id, kind (`webhook|email|telegram|slack`), config_enc, enabled |
| `alert_rules` | id, event_kinds JSON, label_filter (label selector matched against frontend/backend labels), channel_id, min_severity, cooldown_s |
| `settings` | key, value JSON |

Indexes on every foreign key, on `events(ts)`, `audit_log(ts)`, metrics PKs. Keep the whole desired state (minus metrics/events/audit) in an in‑memory snapshot rebuilt on change; API reads hit the snapshot, not SQLite.

---

## 7. HAProxy integration (the core)

### 7.1 Process model, sockets, container
- HAProxy runs in its **own container** from the **official image**, `network_mode: host` (frontends add listeners on arbitrary addresses and ports, which Docker port publishing cannot follow, and host networking avoids the userland proxy/NAT hop).
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
        ├── crt-lists/fe<frontend_id>.lst   # entries relative to crt-base (ADR 0004)
        ├── maps/maint.map            # loaded with opt@, keyed by frontend id, updated at runtime too
        ├── maps/panel_allow.lst
        └── errors/503.http, maint.http
/var/lib/haproxy/server-state         # dump of `show servers state`, written before each reload
/run/haproxy/master.sock, admin.sock
```

- The renderer takes a `Root` parameter. The live file references paths under `/usr/local/etc/haproxy/live/…`; `haproxy.check.cfg` is the same template rendered with the revision's own absolute path, so `haproxy -c` validates the *staged* files before they go live.
- **Atomic swap:** create `live.tmp` → `revs/N`, then `rename(live.tmp, live)`, then fsync the directory.
- Unchanged files are **hard‑linked** from the previous revision (cheap, fast).
- Garbage collection keeps the last 20 revisions plus any `pinned` (rollback) ones.
- Runtime‑only changes (server add, cert renewal) still create a new revision so disk always equals memory.

### 7.3 Naming and stable ids
- Frontend `fe_{F}`; redirect frontend `fe_{F}_rd`; backend `be_{B}`; system sections `sys_panel`, `sys_panel_be`, `sys_stats`, `sys_acme_fe{F}`.
- Frontend and backend names MUST differ (HAProxy 3.3+ rejects a frontend and backend sharing a name) — guaranteed by the `fe_`/`be_` prefixes.
- Server names: `t{T}` for an IP server, `t{T}x{H}` for a host or label‑selector expansion (`T` = `backend_servers.id`, `H` = `hosts.id`). Server `id` = `server_slots.id` (stable), so `load-server-state-from-file` re‑applies weight/admin/health state after reloads.
- Because a server belongs to exactly one backend, a runtime op is one command per resolved server — it never multiplies across frontends.
- Names in HAProxy use ids only; display names, descriptions and labels never appear in the config.

### 7.4 Rendered config (reference output)

Templates live in `internal/haproxy/render/templates/`. Output MUST be deterministic (sorted by id; routes by position) so revisions diff cleanly. Reference rendering for a TLS http frontend with redirect and host/path routing to two backends, plus a tcp frontend/backend pair (deviations recorded in ADR 0004 are already applied):

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

# ---------- frontend 3 "web" (http, tls, redirect on) ----------
frontend fe_3 from loxy_http
    bind 0.0.0.0:443 ssl crt-list /usr/local/etc/haproxy/live/crt-lists/fe3.lst alpn h2,http/1.1
    bind ipv6@:::443 ssl crt-list /usr/local/etc/haproxy/live/crt-lists/fe3.lst alpn h2,http/1.1
    maxconn 10000                             # omitted when maxconn = 0
    timeout client 50s
    option forwardfor
    http-request set-header X-Forwarded-Proto https
    http-request set-header X-Forwarded-Port %[dst_port]
    http-request return status 503 content-type text/html file /usr/local/etc/haproxy/live/errors/maint.http if { str(3),map(opt@/usr/local/etc/haproxy/live/maps/maint.map) -m found }
    use_backend be_5 if { req.hdr(host),field(1,:),lower -m str app.example.com } { path_beg /api }
    use_backend be_6 if { req.hdr(host),field(1,:),lower -m end .static.example.com }
    default_backend be_4

frontend fe_3_rd from loxy_http
    bind 0.0.0.0:80
    bind ipv6@:::80
    http-request return status 200 content-type text/plain lf-string "%[path,field(-1,/),map(virt@acme_http01)]" if { path_beg /.well-known/acme-challenge/ } { path,field(-1,/),map(virt@acme_http01) -m found }
    http-request redirect scheme https code 301

backend be_4 from loxy_http
    balance roundrobin
    timeout server 50s
    option httpchk
    http-check send meth GET uri /health ver HTTP/1.1 hdr Host app.example.com
    http-check expect status 200-399
    http-check expect string 'OK'               # VERIFY AND-semantics, see §7.5
    timeout check 13s
    server t101 10.0.0.11:9000 id 101 check inter 15s rise 3 fall 3
    server t102x7 10.0.0.12:9000 id 102 check inter 15s rise 3 fall 3
    server t102x8 10.0.0.13:9000 id 103 check inter 15s rise 3 fall 3 backup

backend be_5 from loxy_http
    balance leastconn
    server t110 10.0.0.21:8080 id 110 check inter 15s rise 3 fall 3

backend be_6 from loxy_http
    balance roundrobin
    server t111 10.0.0.31:80 id 111 check inter 15s rise 3 fall 3

# ---------- frontend 9 "postgres" (tcp) ----------
frontend fe_9 from loxy_tcp
    bind 10.0.0.5:5432
    maxconn 2000
    tcp-request connection reject if { str(9),map(opt@/usr/local/etc/haproxy/live/maps/maint.map) -m found }
    default_backend be_12

backend be_12 from loxy_tcp
    balance leastconn
    server t120 10.0.0.41:5432 id 120 check inter 15s rise 3 fall 3 send-proxy-v2 check-send-proxy
```

Notes:
- No `master-worker` keyword (deprecated since 3.3; `-W` on the command line is enough). `crt-base` + relative crt‑list entries make each revision validate against its own files (ADR 0004).
- Bind address `*` renders as `0.0.0.0:<port>` + `ipv6@:::<port>`; explicit IPv6 addresses use the `ipv6@addr:port` prefix form (unambiguous).
- Route conditions: each host becomes one `-m str` (exact) or `-m end` (`*.example.com` → `.example.com`) pattern on the lower‑cased Host without port; multiple hosts in a route are OR‑ed (one ACL with several patterns); `path_prefix` adds `{ path_beg <prefix> }` (AND). A route with only a path prefix matches any host. Routes are evaluated in `position` order; first match wins, then `default_backend`. A frontend without a default backend answers unmatched requests with 503.
- The ACME HTTP‑01 rule is rendered in every `fe_<id>_rd` frontend. If a TLS frontend has a managed HTTP‑01 certificate but no redirect, render `sys_acme_fe<id>` on that frontend's addresses:80 that answers challenges and returns 404 otherwise (port collisions with other frontends are rejected by the API, §8.3).
- `maint.map` is an `opt@` map keyed by frontend id: present on disk in the revision **and** updated at runtime with `add map`/`del map`, so maintenance mode is instant and survives reloads. HTTP frontends return the maintenance page with 503; TCP frontends reject the connection. `virt@acme_http01` is a virtual (memory‑only) map because challenge tokens are ephemeral; the reconciler re‑pushes pending tokens after every reload.
- There is no global/per‑object connection cap other than the frontend `maxconn` (0 = not rendered, the global `maxconn` applies).

### 7.5 Field → directive mapping

**Frontend fields**

| Field | http | tcp |
|---|---|---|
| mode | `mode http` (via `from loxy_http`) | `mode tcp` (via `from loxy_tcp`); no header injection; use for TLS passthrough |
| binds [{address, port}] | one `bind` per entry; `*` → all IPv4 + IPv6 | same |
| tls + certificates | `bind … ssl crt-list fe<F>.lst alpn h2,http/1.1`; backends get plain HTTP (TLS termination) | not allowed |
| http_redirect (tls only) | extra `fe_<F>_rd` on the same addresses:80 with ACME rule + `http-request redirect scheme https code 301` | not allowed |
| default_backend_id | `default_backend be_<B>` | same |
| routes | `use_backend be_<B> if …` in position order (§7.4) | not allowed |
| timeout_client_s | `timeout client <n>s` | same |
| maxconn | `maxconn <n>` (omitted when 0) | same |
| forwarded_headers | `option forwardfor`, `X-Forwarded-Proto` (`https` when tls, else `http`), `X-Forwarded-Port` = `%[dst_port]` | not allowed |
| enabled | frontend (and its `_rd` / `sys_acme_fe`) not rendered when false | same |
| maintenance | `http-request return status 503 … maint.http` if id in `maint.map` | `tcp-request connection reject` if id in `maint.map` |

**Backend fields**

| Field | http | tcp |
|---|---|---|
| algorithm | `balance roundrobin|leastconn|source|random` — rendered **explicitly** | same |
| port | default server port (a server's `port` overrides it) | same |
| sticky (cookie name, lifetime) | `cookie <name> insert indirect nocache httponly maxlife <n>s` (+ `secure` when every frontend using the backend has tls), each server `cookie <server name>` | not allowed |
| proxy_protocol v1/v2 | `send-proxy` / `send-proxy-v2` **and** `check-send-proxy` on every server line (otherwise servers that require PROXY reject the health check) | same |
| timeout_server_s | `timeout server <n>s` | same |
| server weight / backup / admin_state | `weight <n>`, `backup`; admin state applied at runtime and kept by server‑state (ADR 0004) | same |

**Health check (backend)**

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

### 7.6 Change classification (runtime vs reload)

| Change | Path | Commands / notes |
|---|---|---|
| Add server (any type) when algorithm ∈ {roundrobin, leastconn, random} | runtime | `add server be_4/t105 1.2.3.4:9000 id 105 check inter 15s rise 3 fall 3 [weight n] [backup] [cookie t105] [send-proxy-v2 check-send-proxy]` → `enable server …` → `enable health …` (dynamic servers start in maintenance with checks off; `default-server` settings are **not** inherited, so render every keyword explicitly; the `enabled` keyword is rejected) |
| Remove server | runtime | `set server <b>/<s> state drain` → wait for `scur=0` or drain timeout (default 30 s) → `set server … state maint` → `wait 5s srv-removable <b>/<s>; del server <b>/<s>` |
| Weight change | runtime | `set server <b>/<s> weight <n>` |
| Drain / maintenance / ready | runtime | `set server <b>/<s> state drain|maint|ready` |
| Label‑selector match set changes (host labels or addresses edited) | runtime | treated as add/remove |
| Server changes in `source` (hash) backends | **reload** | dynamic servers only work with roundrobin, leastconn, first, random |
| Frontend maintenance on/off | runtime | `add map /usr/local/etc/haproxy/live/maps/maint.map <frontend_id> 1` / `del map … <frontend_id>` (+ file in revision). The `opt@` prefix is not part of a map's name, so runtime commands use the plain path; `virt@` *is* part of the name |
| Certificate renewed / replaced (same file) | runtime | `set ssl cert <path> <<\n<pem>\n` → `commit ssl cert <path>` |
| ACME HTTP‑01 token | runtime | `add map virt@acme_http01 <token> <keyauth>` / `del map …` |
| Add / edit / delete / enable / disable frontend | reload | new or changed listeners need a new worker |
| Binds, TLS, redirect, routes, default backend, timeouts, maxconn, forwarded headers | reload | |
| Certificate attached to / detached from a frontend | reload | any crt‑list change is reload‑class (ADR 0004); MAY later use `add/del ssl crt-list` |
| Add / edit / delete backend (algorithm, health check, sticky, proxy protocol, timeouts, port) | reload | |
| Panel allowlist | reload (rare) | MAY use `add acl` at runtime |

Every runtime op also produces the new on‑disk revision. If any runtime command fails, the reconciler falls back to a reload of that revision.

HAProxy 3.4 also allows creating, publishing and deleting whole **backends** at runtime (`add backend … from <defaults>`, `publish backend`, `unpublish backend`, `del backend`). v1 doesn't need it (backends become reachable only through a frontend change, which is a reload anyway); MAY be used later to make "add backend + route" reload‑free once `use_backend` rules can also change at runtime.

### 7.7 Reload procedure
1. `show servers state` → write `/var/lib/haproxy/server-state` atomically.
2. `haproxy -c -f revs/N/haproxy.check.cfg` (inside the loxy container, same HAProxy binary). Non‑zero exit → abort, keep current `live`, record the output.
3. Swap `live` → `revs/N`.
4. Master CLI `reload`; read until EOF; require `Success=1`.
5. On `Success=0`: swap `live` back to the previous revision (so a future cold start is safe), record the startup log, mark the affected frontends/backends `apply_error`, notify. Old workers keep serving the previous config.
6. Post‑checks: `show proc` lists a new worker; `show stat` contains every expected frontend/backend; otherwise treat as failed.
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

---

## 8. Feature specification (screens, behaviour, validation, acceptance)

Global navigation: **Dashboard · Frontends · Backends · Hosts · Certificates · Domains · Activity · System · Settings** (Domains has sub‑pages *Domains*, *DNS providers*, *Zones*). Top bar: project switcher (hidden while only one project exists), global apply status (applying / last error with "Retry", `POST /apply/retry`), user menu, live connection indicator (SSE state).

All destructive actions use a confirm modal; deleting a frontend or backend requires typing its name. Every mutation returns an **action** object (§12.4) whose progress is shown as a toast and in Activity. An apply error shows a banner on the affected frontend/backend and on System with the HAProxy output and "Retry" / "Roll back to last good revision".

### 8.1 Dashboard (`/dashboard`)
- Stat row (live via SSE): frontends (enabled / total, in maintenance), backends by health (healthy / degraded / unhealthy / unknown), servers (healthy / total, draining, in maintenance), certificates expiring < 21 d, current open connections and requests/s across all frontends, bandwidth in/out.
- **Needs attention** list: unhealthy/degraded backends with the first failing checks ("10.0.0.12:9000 · Layer7 wrong status 503"), frontends with apply errors, expiring or failed certificates, domains not resolving correctly.
- Traffic sparkline per top‑5 frontend (last 5 min), last 10 events, node status (HAProxy version, uptime, last reload result).
- Acceptance: renders from `GET /overview` + one SSE stream in < 100 ms after data arrives; no per‑card polling.

### 8.2 Frontends list (`/frontends`)
- Table: status dot (apply status + maintenance + disabled), name, `#id`, mode badge (`HTTP`, `HTTPS` when tls, `TCP`), binds (`*:443`, `10.0.0.5:5432`, "+2"), default backend, routes count, backend health aggregate, live open connections sparkline (last 5 min), req/s, labels.
- Filters: text search, label selector (`env=prod,team!=ops`), mode, status. Sort by name/created/connections.
- **Create frontend** button → form (§8.3). Row ⋯ menu: Edit · Maintenance on/off · Enable/Disable · Duplicate · Delete.
- Empty state explains the model in one line ("A frontend listens on addresses and ports and sends traffic to backends.") with "Create frontend" and "Create backend" actions.
- Acceptance: 100 frontends render in < 100 ms after data arrives; sparklines update via one SSE stream, not per‑row polling.

### 8.3 Frontend create / edit form
Single page with sections; the same form edits an existing frontend (PUT with `If-Match`).
1. **Name, description, labels.**
2. **Mode & TLS** — mode `http | tcp` (segmented control); for http a **TLS termination** toggle; when on: **certificates** picker (chips; existing certificates or create a managed certificate inline from the hostnames of the routes, or upload) and **HTTP → HTTPS redirect (301)** checkbox (default on).
3. **Binds** editor — rows of address + port; the address field is a combobox with `*` (all addresses) and the node addresses (§8.10) as suggestions, any IPv4/IPv6 literal allowed; add/remove rows; templates for the port: 80, 443, custom. Inline collision warnings while typing.
4. **Default backend** — select (filtered to backends of the same mode) or "Create backend…" (opens the backend form in a modal, returns its id).
5. **Routing rules** (http only) — table of rules, drag handle to reorder (order = evaluation order, first match wins): hosts (chip input, exact or `*.example.com`), path prefix (`/api`), backend select; "Add rule". A preview line under each rule reads like the rendered condition ("Host is app.example.com and path starts with /api → api‑backend").
6. **Options** — client timeout (s, default 60), max connections (0 = unlimited), forwarded headers (http, default on), enabled (default on).
- Footer: "Show config preview" (renders the would‑be `fe_<id>` sections via `loxy render --dry-run` logic, read‑only `CodeView`), Cancel, Save. Creation of a frontend with a new default backend happens in one transaction → one reload.
- Validation (server side, mirrored client side):
  - ≥ 1 bind; port 1–65535; address `*` or a valid IP literal (`net/netip`); no duplicate binds within the frontend.
  - **Port collisions across frontends:** a bind collides with another frontend's bind on the same port when the addresses are equal or either is `*`; the check includes every frontend's redirect listener (:80 on its addresses when `http_redirect`), every `sys_acme_fe` :80 listener, and reserved system ports (panel port, `127.0.0.1:8405`, `127.0.0.1:5514`, `127.0.0.1:9090`). Disabled frontends still reserve their ports (so enabling never fails on a collision).
  - `tls` requires mode http and ≥ 1 certificate (a pending managed cert is allowed; a placeholder self‑signed cert is served until issuance).
  - `http_redirect` only with tls, and only when :80 is free on all of the frontend's addresses.
  - Routes require mode http; every route needs ≥ 1 host or a path prefix; hosts are RFC 1123 (`*.` prefix allowed), lowercase, ≤ 20 per route; path prefix matches the path rule of §14.4; ≤ 200 routes per frontend.
  - The default backend and every route's backend must exist and have the **same mode** as the frontend.
  - `timeout_client_s` 1–3600; `maxconn` 0–1 000 000.
- Acceptance: creating an http frontend on `*:8080` with a default backend of 2 IP servers → reachable, both servers healthy, within 3 s of submit on a dev box; a TLS frontend with a managed cert on a domain pointing to the node yields a valid certificate (Pebble in CI) and a 301 from :80 within 60 s, with only one reload.

### 8.4 Frontend detail (`/frontends/[id]/…`)
**Header (all tabs):** editable name (inline), status, `#id`, mode badge, binds (copy on click), maintenance toggle (confirm), ⋯ menu: Edit · Enable/Disable · Duplicate · Export JSON · Copy ID · Delete.

Tabs: **Overview · Routing · Graphs · Logs · Config**.
- **Overview:** stat cards (open connections, conn/s, req/s, bandwidth in/out, live); **Health** card: donut of healthy servers / total across the backends this frontend uses, badge `Healthy | Degraded | Unhealthy | Unknown`, first 3 failing checks with reason; **Binds** card; **TLS** card (certificates with expiry, redirect on/off); **Labels** row (chips + editor, §14.4 rules); **Domains** attached to this frontend with their DNS status (§8.9).
- **Routing:** read view of the default backend and the ordered rules, each with the target backend's health; "Edit routing" opens the routing section of §8.3. Backends link to §8.6.
- **Graphs:** range selector **Live** (rolling 5 min, 1 s points, auto‑updating), 30 min, 1 h, 6 h, 24 h, 7 d, 30 d. Charts: **Open connections**, **Connections per second**, **HTTP requests per second** (http only), **Bandwidth** (in/out; units auto‑scale B/s → GB/s); "Details" (collapsed): HTTP responses per second by class (2xx/3xx/4xx/5xx), denied/errored requests. uPlot with synced cursors; tooltip with exact values and timestamp in the browser's timezone; Live pauses while hovering; no false spikes or dips across reloads (§10.3). Acceptance: switching ranges renders in < 150 ms for 30 d; Live updates every second with ≤ 2 s lag; browser CPU < 5 % on a laptop.
- **Logs:** live log stream of this frontend (sampled, off by default, §20): pause, filter text, status class filter; columns time, client, method + path (http) / bytes, backend/server, status, timings.
- **Config:** read‑only rendered sections of this frontend (`fe_<id>`, `fe_<id>_rd`, `sys_acme_fe<id>`, and the backends it uses) from `GET /frontends/{id}/config`, with "Copy" and "Download".
- Delete: confirm by typing the name; removes the frontend sections (reload), detaches its domains (`frontend_id` set NULL) and certificates (certificates are kept), keeps metrics for 7 days then purges, audit entry. Backends are untouched.

### 8.5 Backends list (`/backends`)
- Table: health badge, name, `#id`, mode, algorithm, servers healthy/total (with draining/maint counts), used by (frontend names), sessions, queue, avg response time, labels.
- Filters: text, label selector, mode, health. **Create backend** → form (name, description, mode, algorithm, default port, health check, then add servers right away).
- Acceptance: 200 backends with 1 000 servers render in < 150 ms after data arrives.

### 8.6 Backend detail (`/backends/[id]/…`)
Header: name, `#id`, mode, algorithm, health badge, "Used by" chips linking to frontends, ⋯ menu: Duplicate · Export JSON · Copy ID · Delete.

Tabs: **Servers · Settings · Graphs**.
- **Servers:**
  - **Add servers** dropdown: **IP** — one or many (paste, one per line, optional `:port`); IPv4/IPv6; rejects loopback (unless `LOXY_DEV=true`), link‑local, multicast, unspecified. **Host** — multi‑select from the inventory with search and label filter; "Use private IP" toggle. **Label selector** — input with syntax `key=value`, `key!=value`, `key in (a,b)`, `key` (exists), comma = AND; live preview "matches N hosts" (`GET /label_selector/preview`). Each with optional port override, weight (default 100) and backup flag.
  - **Grouped / List** toggle. Grouped: each label selector is a group (tag icon, "N hosts", aggregate health, ⋯); host servers grouped as "Hosts"; IP servers as "IP servers". List: flat table of resolved servers.
  - Row columns: name (host name or "—"), address:port, **status** (healthy / unhealthy / draining / maintenance / unknown, with check detail tooltip `L7OK/200 in 3 ms` or failure reason), weight, backup, sessions, ⋯ menu: Drain · Maintenance · Ready · Set weight · Show in graphs · Remove.
  - Removal is graceful (drain → maint → delete, §7.6) and shows progress (`removing`).
  - Empty state: "No servers yet." with the three add options.
  - Acceptance: adding 25 IP servers results in 25 runtime `add server` ops, **no reload**, all active in < 1 s, zero failed requests under load.
- **Settings:** algorithm (Round Robin default, Least Connections, Source, Random; note that Source makes server changes reload‑class), default port; **health check**: protocol (tcp/http/https), port, interval (s), timeout (s), retries, domain, path, response, status codes, TLS verify (https checks); **sticky sessions**: checkbox + cookie name + lifetime (s); **PROXY protocol**: none/v1/v2 with a red warning that enabling it for servers that don't accept PROXY makes the backend unreachable; server timeout (s); labels.
  - Defaults: algorithm roundrobin; hc tcp for tcp backends and http for http backends, interval 15 s, timeout 10 s, retries 3, path `/`, status codes `2??, 3??`; server timeout 60 s; sticky cookie `LOXYSTICKY`, lifetime 300 s; health‑check port = server port.
  - Validation: port 1–65535; `hc_timeout < hc_interval`; interval 3–3600; timeout 1–3600; retries 1–10; http/https checks require a path and are allowed on tcp backends too; HTTP fields are returned as null for tcp checks; sticky only for http mode; changing `mode` is refused while the backend is used by a frontend of the other mode.
- **Graphs:** same range selector as §8.4; charts **Sessions** (current), **Queue**, **Average response time (ms)**, **HTTP responses per second by class** (http), **Errors per second** (connection, response, retries/redispatches); filter: whole backend / one server.
- Delete: refused with `409 conflict` while the backend is a default backend or a route target of any frontend (message lists them); otherwise confirm by typing the name → reload, server slots released, metrics kept 7 days.

### 8.7 Hosts (inventory, `/hosts`)
- CRUD of hosts: name, IPv4, IPv6, private IPv4, labels, notes. Bulk import from CSV (`name,ipv4,ipv6,private_ipv4,labels`).
- Shows where each host is used (backends, directly or via a label selector) and its current health there.
- Editing a host's labels or addresses updates every label‑selector / host server at runtime (§7.6). Deleting a host that is referenced directly by a server is refused; selector matches simply drop out (graceful removal).

### 8.8 Certificates (`/certificates`)
- List: name, type (Uploaded/Managed), domains, status (Pending/Issued/Renewing/Failed), expires in (amber < 21 d, red < 7 d), used by N frontends, labels.
- **Upload**: paste or file for certificate, chain, private key. Validate: key matches cert, not expired, chain completes to a root in the system bundle (warn only), reorder chain if needed. Key stored encrypted at rest.
- **Create managed**: domains (≤ 100), challenge HTTP‑01 (default; domains must resolve to a frontend that serves the challenge on :80 — its redirect frontend or `sys_acme_fe`) or DNS‑01 (pick a connected DNS provider from §8.9 — credentials are not entered again; required for wildcards), key type ECDSA P‑256 (default) / RSA 2048/4096.
- Detail drawer: SANs, issuer, serial, fingerprint, validity, last issuance log, renewal schedule, frontends using it. Actions: Renew now · Download chain (never the key unless owner role + re‑auth) · Delete (blocked while attached to a frontend).

### 8.9 Domains & DNS (`/domains`)

Loxy helps operators point names at their frontends and keeps DNS in sync, **without running its own DNS server** (non‑goal, §1.3). All record changes go through the APIs of external DNS providers using the **libdns** ecosystem (`github.com/libdns/*`).

**Domains page** (`/domains`)
- List: FQDN, attached frontend, DNS provider (or "manual"), status badge (**Resolves correctly** / **Wrong IP** / **Missing** / **Unknown**), last checked, certificate (issued / pending / none), auto‑managed records on/off.
- **Add domain**: FQDN (validated per §14.4), choose a frontend (required), optional DNS provider (auto‑detected when a connected provider owns a matching zone), "Manage A/AAAA records automatically" checkbox (only with a provider; Phase 2).
- **Required records** are derived from the frontend's binds: each explicit bind address gives `A <fqdn> → <ipv4>` / `AAAA <fqdn> → <ipv6>`; when the frontend binds `*`, the node's addresses (§8.10, public ones only — loopback/private are skipped unless they are the only ones) are used instead. TTL 300, with copy buttons.
- Detail drawer: required records table; **live verification**: Loxy resolves the name (A and AAAA, via the system resolver and, when a provider is attached, also by reading the provider's records) and shows per family: ✓ resolves correctly, ✗ wrong IP (shows the observed addresses), ✗ missing.
- Verification runs on add, on "Check now", every 5 minutes for unverified domains and every hour for verified ones, and whenever the frontend's binds or the node addresses change. Status changes emit `domain.status.changed` events.
- **Issue certificate** (one click) is enabled once the domain is verified: creates a managed certificate for the FQDN (HTTP‑01 if the name resolves to the frontend and it has a :80 listener, or DNS‑01 if a provider is attached — required for wildcards) and attaches it to the frontend when it terminates TLS.

**DNS providers** (`/domains/providers`)
- Connect provider accounts through libdns provider modules (first: the providers listed in §21 Q4; each with its own token fields, e.g. a scoped API token with zone‑read and DNS‑edit rights). The registry is a map `kind → {fields schema, constructor}` so new libdns providers are added in one file; `GET /dns_providers` also returns the available kinds and their fields.
- Credentials are encrypted at rest with AES‑256‑GCM (`LOXY_SECRET_KEY`, §14.5), never returned by the API (only `"configured": true`), and tested on save with a `ListZones` call (status `ok|error`).
- **ACME DNS‑01 reuses the same credentials**: `internal/dns` implements lego's `challenge.Provider` (`Present`/`CleanUp`) on top of the libdns `RecordAppender`/`RecordDeleter` interfaces, writing `_acme-challenge.<name> TXT <value>` (TTL 60) into the right zone and deleting exactly that record afterwards. Certificates reference a `dns_provider_id` instead of carrying their own credentials, so credentials live in one place.

**Auto‑managed records** (Phase 2)
- Opt‑in per domain. When on and a provider is connected, Loxy creates the A/AAAA records for the FQDN pointing at the required addresses, **updates** them when they change (frontend binds edited, node addresses edited, domain moved to another frontend), and **removes** them when the domain is detached or deleted or its frontend is deleted.
- Loxy only touches records it created (their provider IDs are tracked in `domains.managed_record_ids`); pre‑existing records with the same name are reported as a conflict and never overwritten without explicit confirmation.
- Sync runs through the reconciler's event stream (debounced) and is retried with backoff; failures surface as `domain.sync.failed` events.

**Zone editor** (Phase 2, `/domains/zones`)
- List zones from all connected providers; open a zone to list and edit records: types **A, AAAA, CNAME, TXT, MX, CAA, SRV, NS**; columns type, name (relative, `@` for apex), value, TTL; add / edit / delete with confirm.
- Server‑side validation per type: A/AAAA parsed with `net/netip`; CNAME/NS/MX targets are RFC 1123 hostnames; MX priority 0–65535; SRV `priority weight port target`; CAA `flags tag "value"` with tag ∈ `issue|issuewild|iodef`; TXT ≤ 255 chars per string (longer values are split); TTL 60–86400; CNAME cannot coexist with other records of the same name. Records managed by Loxy are marked and edits to them warn.

**API** (§12.2): `/domains`, `/domains/{id}/actions/{verify,issue_certificate}`, `/dns_providers`, `/dns_providers/{id}/zones`, `/dns_providers/{id}/zones/{zone}/records`.

**Acceptance**
- Adding a domain whose A record points to the frontend's bind address shows "Resolves correctly" within one check; a wrong A record shows "Wrong IP" with the observed address; for a `*` frontend the node addresses are the expected values.
- With a DNS provider connected, a managed DNS‑01 certificate for `*.example.com` is issued (Pebble + a libdns test double in CI) without entering credentials again.
- With auto‑records on, changing the frontend's bind IPv4 updates the A record within 10 s; deleting the domain removes exactly the records Loxy created.

### 8.10 System (`/system`)
- **Node** card (all‑in‑one shows the local node): status, HAProxy version and feature flags (`haproxy -vv`: QUIC, PROMEX, OpenSSL version), loxy version, HAProxy uptime, reload count and last reload result/duration, current connections, CPU/memory of both containers; warning when the bundled `haproxy -v` differs from the running one.
- **Node addresses**: list of the node's IPv4/IPv6 addresses with an optional label (`POST/DELETE /nodes/{id}/ips`); detected interface addresses are offered for one‑click adding. Used as bind‑address suggestions (§8.3) and as DNS required records for `*` binds (§8.9). Changing them never changes the HAProxy config by itself.
- **Config revisions**: list (revision, kind runtime/reload/rollback/bootstrap, result, duration, author, time, pinned), diff between any two (`CodeView`), "Roll back to this revision" (break‑glass swap + reload of the on‑disk revision, see ADR 0004 §7; the next mutation re‑applies desired state, which the UI states), pin/unpin, HAProxy output of failed applies.
- Phase 3 adds enrollment, multiple nodes and HA groups (§15).

### 8.11 Activity (`/activity`)
- **Events** feed (server/backend health transitions, apply/reload results, cert issued/failed/expiring, domain status changes, node offline) with filters by frontend/backend/kind/severity.
- **Audit log** (who, what, when, before/after diff, IP, token) — read‑only, exportable CSV.

### 8.12 Settings (`/settings/*`)
Users & roles · API tokens · ACME (account email, directory: Let's Encrypt production / staging / custom, EAB) · Notification channels & alert rules (event kinds, label filter on frontend/backend labels, min severity, cooldown, "Send test") · Two‑factor authentication (own TOTP; enforcement per project) · Panel (domain, port, allowlist, session lifetime) · System (hard‑stop‑after, drain timeout, reload min interval, metrics retention, live‑log sampling) · Backup (schedule, retention, optional S3‑compatible target).

---

## 9. Health model

Source: `show stat` server rows (`status`, `check_status`, `check_code`, `check_duration`, `check_desc`/`last_chk`, `lastchg`).

| HAProxy server `status` | loxy server status |
|---|---|
| `UP` | healthy |
| `UP n/m` (going down) | healthy (transitioning) |
| `DOWN` | unhealthy |
| `DOWN n/m` (going up) | unhealthy (transitioning) |
| `NOLB` | healthy, not receiving new traffic |
| `MAINT`, `MAINT (via)`, `MAINT (resolution)` | maintenance |
| `DRAIN` | draining |
| `no check` | unknown |

Aggregation: health is tracked **per resolved server** (one check each, since a server belongs to one backend). Backend status = `Healthy` if all active (non‑maintenance) servers are healthy, `Degraded` if some are unhealthy but ≥ 1 is healthy, `Unhealthy` if none is healthy, `Unknown` without servers or checks. A frontend's health summary aggregates the backends it uses (default + routes). Transitions produce `server.health.changed` / `backend.health.changed` events (with check reason) used by notifications; debounce flapping with the alert‑rule cooldown. Optionally enable `option log-health-checks` and parse health transitions from the UDP log stream for sub‑second detection.

---

## 10. Metrics pipeline

### 10.1 Collection
Every 1 s per node: `show stat` (CSV; parse the header line to map columns by name, never by position) and `show info`. Frontend rows (`fe_<id>`, plus `fe_<id>_rd` summed into its frontend) → frontend metrics; backend rows (`be_<id>`) → backend metrics; server rows → per‑server metrics and health. One `show stat` call returns everything; it is cheap even with thousands of servers.

### 10.2 Derived series

**Per frontend**

| Series | Derivation |
|---|---|
| open_connections | frontend `scur` |
| connections_per_second | Δ frontend `conn_tot` / Δt (fallback `conn_rate`) |
| requests_per_second | Δ frontend `req_tot` / Δt (http frontends only) |
| bandwidth_in / bandwidth_out | Δ frontend `bin` / Δt, Δ `bout` / Δt (bytes/s) |
| http_1xx…5xx per second | Δ frontend `hrsp_*` / Δt |
| denied / errors per second | Δ frontend `dreq` + `ereq` / Δt |

**Per backend (and per server)**

| Series | Derivation |
|---|---|
| sessions | backend/server `scur` |
| queue | backend `qcur` (server `qcur`) |
| response_time_ms | backend `rtime` (moving average HAProxy maintains), `ttime` |
| http_1xx…5xx per second | Δ backend `hrsp_*` / Δt |
| errors per second | Δ backend `econ` + `eresp` / Δt; retries `wretr`, redispatches `wredis` |
| bandwidth_in / bandwidth_out | Δ backend `bin` / `bout` / Δt |

### 10.3 Counter resets
New workers start counters at zero. Detect a new worker via `show info` `Pid`/`Uptime_sec` change or any counter decreasing; for that sample use `delta = current` (not negative, not a spike). Connections still served by old workers during drain are not counted — accepted. HAProxy 3.3+ has an experimental `shm-stats-file` that can preserve counters across reloads; MAY be enabled behind a setting after a **VERIFY** test.

### 10.4 Storage and retention
- In memory: ring buffer of 1 s points for the last 60 min per frontend and per backend (≈ 3 600 × ~12 series × 8 B ≈ 350 KB per object at worst; keep per‑server series only for the last 15 min).
- SQLite rollups written in one transaction per minute: `metrics_1m` (scope `frontend|backend`, scope_id), with 5 m / 1 h resolutions aggregated on read or written by a later rollup job (ADR 0001 §5). Each rollup stores avg and max where meaningful.
- Range → resolution: Live/30 min → 1 s (downsample to ≤ 1 800 points), 1 h → 10 s (from memory), 6 h/24 h → 1 m, 7 d → 5 m, 30 d → 1 h.

### 10.5 API format
Columnar, uPlot‑native:
```json
{ "start": 1760412300, "end": 1760412600, "step": 1,
  "timestamps": [1760412300, 1760412301, ...],
  "series": { "open_connections": [2210, 2215, ...], "connections_per_second": [41, 18, ...],
              "requests_per_second": [8820, 8790, ...], "bandwidth.in": [4.1e6, ...], "bandwidth.out": [4.3e6, ...] } }
```
History: `GET /api/v1/frontends/{id}/metrics?type=…` and `GET /api/v1/backends/{id}/metrics?type=sessions,queue,response_time_ms,…`. Live: `GET /api/v1/frontends/{id}/metrics/stream` (SSE) pushes one compact point per second.

### 10.6 Optional exports
- `sys_stats` frontend on `127.0.0.1:8405`: HAProxy's built‑in Prometheus exporter at `/metrics` (render only if `haproxy -vv` shows PROMEX) and the native stats page at `/stats`. Because names are `fe_<id>`/`be_<id>`, loxy's own `/metrics` exposes an info series mapping ids to display names and labels.
- loxy's own `/metrics` (controller health: reload durations, apply errors, poll latency, ACME results).

---

## 11. Certificates and ACME

### 11.1 Storage
Certificates are project‑level and reusable across frontends. Private keys are encrypted at rest (AES‑256‑GCM, key from `LOXY_SECRET_KEY`). The rendered revision necessarily contains plaintext PEMs (HAProxy reads them); the config volume is mode 0750, files 0640, group `loxy` (§16).

### 11.2 Managed certificates with lego
- One ACME account per directory URL (`acme_accounts`), created lazily with the configured email (and EAB when required by the CA).
- **HTTP‑01**: implement lego's `challenge.Provider` (`Present(ctx, domain, token, keyAuth)` / `CleanUp(...)`). `Present` pushes `add map virt@acme_http01 <token> <keyAuth>` to **every node** that hosts a frontend serving that domain, waits for acknowledgement, then returns; `CleanUp` deletes the entry. HAProxy answers the challenge itself with the `http-request return … map(virt@acme_http01)` rule in `fe_<id>_rd` / `sys_acme_fe<id>` (§7.4) — no extra hop, no port juggling.
- **DNS‑01**: a lego `challenge.Provider` implemented on top of the libdns provider of a connected DNS provider account (§8.9), so DNS credentials are stored once (encrypted) and shared by record management and ACME; required for wildcard domains.
- Key type default ECDSA P‑256; RSA optional.
- Issuance runs as an async action; failures record the lego error and retry with backoff (1 m, 5 m, 30 m, 2 h, then every 6 h) without blocking the frontend (placeholder cert keeps serving).
- **Renewal** scheduler: honour ACME Renewal Information (ARI) when the CA offers it; otherwise renew at 2/3 of the lifetime (≈ 30 days before expiry for 90‑day certs). Jitter start times. On success: new revision with the new PEM + runtime `set ssl cert` / `commit ssl cert` (no reload).
- Use Let's Encrypt **staging** automatically in dev and CI (Pebble in tests).
- HAProxy's native ACME (`acme` section, 3.2+) MAY replace lego later once it's no longer experimental; keep the `CertIssuer` interface abstract so that's a swap, not a rewrite.

### 11.3 Uploaded certificates
Same runtime hot‑swap path when replaced. Expiry warnings at 21/7/1 days.

---

## 12. REST API

The contract is `api/openapi.yaml` (OpenAPI 3.0.3); this section summarises it. When they disagree, fix one of them in the same PR.

### 12.1 Conventions
- Base path `/api/v1`, JSON, `snake_case`, plural resource names (`frontends`, `backends`, `servers`, `hosts`, `certificates`, `domains`, `actions`, `metrics`).
- Auth: session cookie (UI) or `Authorization: Bearer loxy_<prefix>_<secret>` (API tokens).
- Pagination: `?page=&per_page=` (max 100) with `meta.pagination`. Filtering: `label_selector=`, `name=`. Sorting: `sort=name:asc`.
- Optimistic concurrency: every mutable resource has `version`; PUT/PATCH honour `If-Match` (ADR 0001 §6); mismatch → `409 conflict`.
- Errors: `{ "error": { "code": "invalid_input", "message": "...", "details": { "fields": [{ "name": "binds[0].port", "messages": ["already used by frontend web (#3)"] }] } } }`. Codes: `invalid_input`, `conflict` (incl. in‑use deletes), `protected`, `not_found`, `forbidden`, `unauthorized`, `rate_limited`, `apply_failed`.
- Idempotency: POSTs accept `Idempotency-Key`.

### 12.2 Endpoints

| Method & path | Purpose |
|---|---|
| `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, `POST /auth/password`, `POST /auth/totp/{setup,enable,disable}` | Session auth, password change, 2FA |
| `GET /overview` | Dashboard counts: frontends, backends, servers, certificates, node, traffic |
| `GET/POST /frontends`, `GET/PUT/PATCH/DELETE /frontends/{id}` | Frontend CRUD (PUT: full object incl. binds, certificates, routes; PATCH: name, description, labels, enabled, maintenance) |
| `GET /frontends/{id}/config` | Rendered HAProxy sections for this frontend and the backends it uses (read‑only) |
| `GET /frontends/{id}/metrics?type=…&start=&end=&step=`, `GET /frontends/{id}/metrics/stream` | History (§10.5), SSE live points |
| `GET /frontends/{id}/logs/stream` | SSE live logs (when enabled) |
| `GET/POST /backends`, `GET/PUT/DELETE /backends/{id}` | Backend CRUD (DELETE → `409` while in use) |
| `GET /backends/{id}/status` | Health: summary + resolved servers with check details |
| `GET /backends/{id}/metrics?type=…` | Backend history |
| `GET/POST /backends/{id}/servers`, `PATCH/DELETE /backends/{id}/servers/{sid}` | Servers (POST takes a list; PATCH: weight, admin_state, backup; DELETE is graceful) |
| `GET/POST /hosts`, `PATCH/DELETE /hosts/{id}` | Inventory |
| `GET /label_selector/preview?selector=` | Hosts matched by a selector |
| `GET/POST /certificates`, `GET/PATCH/DELETE /certificates/{id}`, `POST /certificates/{id}/actions/renew` | Certificates |
| `GET/POST /domains`, `GET/PATCH/DELETE /domains/{id}`, `POST /domains/{id}/actions/verify`, `POST /domains/{id}/actions/issue_certificate` | Domains (§8.9) |
| `GET/POST /dns_providers`, `DELETE /dns_providers/{id}`, `GET /dns_providers/{id}/zones`, `GET/POST/DELETE /dns_providers/{id}/zones/{zone}/records` | DNS providers (incl. available kinds) and zone editor (§8.9; records Phase 2) |
| `GET /nodes`, `PATCH /nodes/{id}`, `POST /nodes/{id}/ips`, `DELETE /nodes/{id}/ips/{ipid}` | Node info and node addresses |
| `GET /nodes/{id}/revisions`, `GET /nodes/{id}/revisions/{rev}/diff?against=`, `POST /nodes/{id}/revisions/{rev}/actions/rollback` | Config revisions |
| `POST /apply/retry` | Re‑run the reconciler after an apply error |
| `GET /actions`, `GET /actions/{id}` | Async action status |
| `GET /events` (SSE), `GET /events/history` | Event stream & history |
| `GET /audit` | Audit log |
| `GET/POST/DELETE /users`, `/api_tokens`, `/notification_channels` (+ `POST …/{id}/actions/test`), `/alert_rules`, `GET/PATCH /settings` | Admin |
| `GET /healthz`, `GET /readyz`, `GET /metrics` | Ops (no auth on healthz; readyz checks DB + HAProxy socket) |

### 12.3 Example resources
```json
{
  "frontend": {
    "id": 3, "name": "web", "description": "public site", "version": 14,
    "mode": "http", "tls": true, "http_redirect": true, "certificates": [12],
    "binds": [ { "address": "*", "port": 443 } ],
    "default_backend_id": 4,
    "routes": [
      { "id": 1, "hosts": ["app.example.com"], "path_prefix": "/api", "backend_id": 5 },
      { "id": 2, "hosts": ["*.static.example.com"], "path_prefix": "", "backend_id": 6 }
    ],
    "timeout_client_s": 50, "maxconn": 10000, "forwarded_headers": true,
    "enabled": true, "maintenance": false, "labels": { "env": "prod" },
    "status": "active", "last_error": null, "created_at": 1760412300000,
    "stats": { "open_connections": 2210, "requests_per_second": 8820, "bytes_out_per_second": 4.3e6 },
    "health": { "status": "healthy", "healthy": 4, "total": 4, "messages": [] }
  }
}
```
```json
{
  "backend": {
    "id": 4, "name": "app", "version": 7, "mode": "http", "algorithm": "roundrobin", "port": 9000,
    "health_check": { "protocol": "http", "port": 9000, "interval_s": 15, "timeout_s": 13, "retries": 3,
                      "http": { "domain": "app.example.com", "path": "/health", "response": "OK", "status_codes": ["2??", "3??"], "tls_verify": false } },
    "sticky_sessions": false, "cookie_name": "LOXYSTICKY", "cookie_lifetime_s": 300,
    "proxy_protocol": "none", "timeout_server_s": 50, "labels": { "env": "prod" },
    "used_by": [3], "servers_count": 3,
    "health": { "status": "healthy", "healthy": 3, "total": 3, "messages": [] },
    "stats": { "sessions": 180, "queue": 0, "response_time_ms": 12 }
  }
}
```

### 12.4 Actions
Long or multi‑step operations return `202` with `{ "action": { "id", "command", "status": "running|success|error", "progress": 0-100, "resources": [...], "error": null, "started", "finished" } }`. The UI follows actions over the `/events` SSE stream (no polling).

### 12.5 SSE event kinds
`frontend.updated`, `backend.updated`, `apply.started|succeeded|failed`, `server.health.changed`, `backend.health.changed`, `certificate.updated`, `domain.status.changed`, `domain.sync.failed`, `action.updated`, `node.status.changed`, `metrics.tick` (only on metric streams). Reconnect with `Last-Event-ID`; server keeps a 2‑minute replay buffer.

### 12.6 Codegen
`make gen` runs `oapi-codegen` (Go server interfaces + models) and `openapi-typescript` (web types). CI fails if generated files are stale.

---

## 13. Frontend implementation

- SvelteKit SPA (`adapter-static`, fallback `index.html`, `ssr = false`), served by loxy from `go:embed` with precompressed `.br`/`.gz` assets, `Cache-Control: public, max-age=31536000, immutable` for hashed files and `no-cache` for `index.html`.
- Routes mirror the tabs so every tab is linkable (`/frontends/3/routing`, `/backends/4/servers`).
- Data: one session‑scoped snapshot store fed by REST on navigation and kept fresh by the single `/events` SSE connection; mutations are optimistic where safe (labels, names) and action‑driven otherwise.
- Charts: `Chart.svelte` wraps uPlot, dynamically imported only on the Graphs tabs (sparklines on the Dashboard and lists use a 1 KB SVG polyline component, not uPlot).
- Components: `StatBar`, `StatCard`, `Donut`, `HealthBadge`, `Tabs`, `Table` (virtualised above 200 rows), `Modal`, `Dropdown`, `Toast`, `LabelChips`, `LabelSelectorInput`, `PortField`, `BindsEditor`, `RouteTable` (drag to reorder), `BackendSelect`, `CertPicker`, `HostPicker`, `CopyText`, `ConfirmTyped`, `CodeView` (read‑only config/diff viewer, no heavy editor).
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
- Optional TOTP 2FA, enforceable per project.
- Login rate limit: 5 attempts / 15 min per IP + per account, exponential backoff.

### 14.2 Authorization
Roles: **owner** (everything incl. users, secrets), **admin** (all resources, settings except owner transfer), **operator** (frontends, backends, servers, hosts, certificates, domains; no users/settings), **viewer** (read‑only). API tokens carry a role and optional expiry; shown once at creation; stored as SHA‑256 hash with a visible prefix.

### 14.3 Web hardening
CSRF: SameSite=Strict cookies **and** a required `X-LOXY-CSRF` header token on mutating requests. Headers: strict CSP (`default-src 'self'`), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `Frame-Options: DENY`, HSTS on the panel. Request body limit 1 MB (5 MB for cert upload).

### 14.4 Input validation (config‑injection defence)
User values end up inside `haproxy.cfg`. **Never** interpolate raw text. Validate with strict allow‑lists, then render through a typed template layer that quotes per HAProxy rules (single quotes; reject values containing quotes).

| Field | Rule |
|---|---|
| Resource display names | 1–64 chars, any printable; **never rendered** into config |
| Label keys | `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?[a-zA-Z0-9]([-a-zA-Z0-9_.]{0,61}[a-zA-Z0-9])?$` |
| Label values | `^([a-zA-Z0-9]([-a-zA-Z0-9_.]{0,61}[a-zA-Z0-9])?)?$` |
| Domains (health check Host, cert SANs, route hosts) | RFC 1123 hostname, lowercase, ≤ 253; `*.` prefix only for DNS‑01 certificates and route hosts |
| Health check path, route path prefix | `^/[A-Za-z0-9._~!&()*+,;=:@%/?-]{0,254}$` (no spaces, quotes, `#`, `$`, `\`) |
| Health check response string | printable ASCII except `'"\#$`, 1–128 chars |
| Cookie name | `^[A-Za-z0-9_-]{1,64}$` |
| Ports | integers 1–65535 |
| Bind address | `*` or an IP literal parsed with `net/netip` (zone ids rejected) |
| IPs / CIDRs | parsed with `net/netip`; servers reject loopback, link‑local, multicast, unspecified |
| Status code items | `^[1-5][0-9?]{2}$`, ≤ 20 items |
| Label selector | parsed by our own small parser into an AST; never passed to HAProxy |

Tests: a Go fuzz target generates random valid models and asserts the rendered config (a) passes `haproxy -c`, (b) contains no line not produced by a known template block, (c) never contains a newline inside a value.

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
- Frontends are placed on a node or an HA group; backends are rendered on every node that has a frontend using them.
- Messages: panel → agent `DesiredState{version, haproxy config inputs, certs, acme_tokens}`; agent → panel `Applied{version, result, output}`, `StatsBatch` (1 s samples, sent every 1 s), `HealthDelta`, `NodeInfo`.
- The agent persists the last applied desired state locally and keeps running autonomously if the panel is down (HAProxy keeps serving; ACME HTTP‑01 tokens pushed before disconnect stay valid).

### 15.2 HA pairs (active/passive, generic)
- Both nodes of an HA group receive identical desired state for the group's frontends; both may bind the group's **virtual IPs** at all times (`net.ipv4.ip_nonlocal_bind=1` and `net.ipv6.ip_nonlocal_bind=1` on the host), so failover needs no reload.
- Leader election and address movement: **keepalived** VRRP (in **unicast** mode between the nodes' private addresses by default; multicast optional) owns the virtual IP(s) and moves them to the backup node; a `track_script` checks HAProxy (`/healthz` of the agent + a TCP probe of a frontend port). `notify_*` scripts report state changes to loxy (`loxy ha notify <state>`), which records events. No provider API is called; environments that need provider‑specific address routing can hook their own script into `notify_master`.
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
(If `nf_conntrack` is loaded, raise `net.netfilter.nf_conntrack_max` accordingly or exclude frontend ports from tracking.)

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
| `LOXY_LISTEN` | `127.0.0.1:9090` | Internal HTTP listener |
| `LOXY_PANEL_PORT` | `8443` | Port of the `sys_panel` frontend |
| `LOXY_PANEL_DOMAIN` | empty | If set, managed cert for the panel |
| `LOXY_PANEL_ALLOW` | empty | Comma‑separated CIDRs allowed to reach the panel |
| `LOXY_ACME_EMAIL` | empty | ACME account contact |
| `LOXY_ACME_DIRECTORY` | Let's Encrypt production | Directory URL |
| `LOXY_DATA_DIR` | `/var/lib/loxy` | DB, backups |
| `LOXY_HAPROXY_CONF_DIR` | `/usr/local/etc/haproxy` | Revision tree |
| `LOXY_HAPROXY_RUN_DIR` | `/run/haproxy` | Sockets |
| `LOXY_STATS_INTERVAL` | `1s` | Poll interval |
| `LOXY_LOG_LEVEL` | `info` | |

### 16.5 Installer (`deploy/install.sh`)
1. Check root, Docker ≥ 24, Compose v2, free ports (8443, 80/443 if they will be used by frontends).
2. Write `/etc/sysctl.d/99-loxy.conf`, `sysctl --system`.
3. Create `/opt/loxy/{data/haproxy-conf,data/haproxy-state,data/loxy,run/haproxy}`; `chown -R 1500:1500`; `chmod 0750` on data dirs and `chmod 0770` on `run/haproxy` (the capability‑dropped HAProxy master creates its sockets there through group 1500).
4. Write `compose.yml` and `.env` (generate `LOXY_SECRET_KEY`, chmod 0600).
5. `docker compose pull && docker compose up -d`; wait for healthy.
6. Print panel URL (`https://<host>:8443`) and the one‑time owner password (`docker compose logs loxy | grep 'initial password'`), remind to restrict access (allowlist/firewall).

### 16.6 Operations
- **Upgrade loxy:** `docker compose pull loxy && docker compose up -d loxy` — HAProxy keeps serving, no traffic impact. DB migrations run at start after an automatic pre‑migration backup.
- **Upgrade HAProxy:** bump `HAPROXY_VERSION` (and the matching loxy image tag) → `docker compose up -d`. Replacing the HAProxy container restarts it (brief interruption) — schedule it, or in an HA pair fail over first, upgrade passive, fail back.
- **Backup:** `loxy backup` (SQLite online backup via `VACUUM INTO` + current live revision tarball), nightly by default, keep 14; optional upload to S3‑compatible storage. **Restore:** `loxy restore <file>` then the reconciler re‑applies.
- **Break‑glass:** `docker compose exec loxy loxy admin reset-password`; `loxy render --dry-run` prints the config that would be applied; `loxy admin rollback --revision N`.
- **Firewall:** allow frontend bind ports publicly; restrict 8443 to admin IPs; never expose 9090, 8405 or 5514.

---

## 17. Development workflow

- `make dev` → `docker compose -f deploy/compose/compose.dev.yml up` with: HAProxy (same image), three `traefik/whoami` servers on different ports (one can be toggled unhealthy via an env flag), **Pebble** (Let's Encrypt's ACME test server) + its challenge DNS, and loxy in dev mode with live reload; Vite dev server proxies `/api` to loxy.
- `make gen` (OpenAPI → Go + TS, sqlc), `make lint` (golangci‑lint, `svelte-check`, eslint, prettier), `make test`, `make test-integration`, `make e2e`, `make load`, `make build`, `make image`.
- `make seed` creates a demo project: an http, a TLS (redirect + routes) and a tcp frontend, 3 backends, 6 servers (IP, host and label selector), 3 hosts, an uploaded cert, a managed cert against Pebble, and synthetic traffic (`oha` loop) so graphs have data.
- Conventional commits; CI on every PR: gen‑check, lint, unit, integration (real HAProxy via testcontainers), e2e (headless), bundle‑size check, image build.

---

## 18. Testing strategy

| Layer | What |
|---|---|
| Unit | Validation rules (incl. cross‑frontend port collisions with `*`, redirect :80 and `sys_acme_fe` :80; frontend/backend mode match; in‑use backend delete refusal); status‑code conversion; label‑selector parser; route → ACL rendering; metric delta/reset logic; health aggregation; reconciler classification table (§7.6) |
| Renderer golden tests | Fixture models → expected `haproxy.cfg` (byte‑exact): tls frontend with redirect and routes, tcp pair, `*` binds, maintenance, disabled frontend, `sys_acme_fe`; every golden passes `haproxy -c` in CI |
| Fuzz | Random valid models → config passes `haproxy -c`, no injected lines (§14.4) |
| Integration (real HAProxy 3.4) | Runtime client matchers for every command; reload success and failure paths (`Success=0` keeps old workers, symlink restored); server‑state survives reload; dynamic server add/remove/drain; host/path routing reaches the right backend; maintenance via map (http 503, tcp reject); cert hot‑swap; ACME HTTP‑01 + DNS‑01 against Pebble; metrics deltas across reloads; **all VERIFY items**: `-S` socket options, double `http-check expect` semantics, `@system-ca`, `del ssl cert`, minimal capability set, `chroot auto`, prompt mode, `shm-stats-file` (if enabled) |
| E2E (Playwright) | Login, create backend + servers of all three types, create http / TLS (with redirect) / tcp frontends, add routing rules and reorder them, toggle sticky/proxy protocol, drain/maint/weight a server, maintenance mode, delete refused for an in‑use backend, delete frontend, certificate upload/managed flow, domain verification, graphs render, rollback |
| Load / zero‑downtime | `oha` at 2 000 rps against a frontend while (a) 100 reloads, (b) 50 server add/removes, (c) cert renewals — **0 non‑2xx/connection errors** allowed |
| Resource | loxy RSS and CPU under the §1.4 dataset (50 frontends / 200 backends / 1 000 servers); UI bundle size budget |

---

## 19. Roadmap and Definitions of Done

**Phase 0 — Foundations** *(done)*
Repo skeleton, Makefile, CI, OpenAPI skeleton + codegen, SQLite + migrations, auth (owner login, sessions), runtime client + parsers, renderer + golden tests, revision FS + validator, bootstrap config, reload procedure.
*DoD:* `docker compose up` on a clean VM brings up HAProxy with the panel reachable at :8443; `loxy render --dry-run` works; reload path integration tests pass.

**Phase 1 — HAProxy management MVP on a single node**
Frontends (http/tcp, binds incl. `*`, TLS termination, HTTP→HTTPS redirect, default backend, host/path routes, timeouts, maxconn, forwarded headers, enable, maintenance, labels); backends (algorithm, full health checks, sticky, PROXY protocol, timeouts, labels); servers (IP, host, label selector) with runtime apply, weight/backup/drain/maint and graceful removal; hosts inventory; certificates (upload + managed HTTP‑01/DNS‑01); **Domains & DNS part 1** (§8.9): domains attached to frontends with required records and live verification, DNS provider accounts with encrypted credentials, DNS‑01 via the libdns‑backed lego provider, one‑click certificate issuance; node addresses; Dashboard, frontend Overview/Routing/Graphs/Config, backend Servers/Settings/Graphs (live + history); Activity (events + audit); users/roles.
*DoD:* every acceptance criterion in §8 for these screens passes in E2E; zero‑downtime load tests pass; §1.4 budgets met.

**Phase 2 — Operations polish**
Config revisions UI (list, diff, rollback, pin), notifications (webhook, email, Telegram, Slack) and alert rules with label filters, API tokens, live logs (sampled) on the frontend Logs tab, Prometheus endpoints, backups (+ S3), 2FA, `loxy` CLI client for the API, **Domains & DNS part 2** (§8.9): auto‑managed A/AAAA records and the zone editor.
*DoD:* alert on server down arrives within 5 s; rollback restores previous behaviour verified by integration test; nightly backup + restore round‑trip test.

**Phase 3 — Multi‑node and generic HA**
`loxy panel` / `loxy agent`, enrollment, node pages, HA groups with keepalived VRRP and a virtual IP (no provider API), `peers` sync of stick tables.
*DoD:* killing the active node moves the virtual IP and traffic to the passive node automatically; the panel going down doesn't affect traffic; agent reconnects and reconciles.

**Phase 4 — Advanced L7 (optional)**
Rate limiting per client IP (stick tables), IP allow/deny lists per frontend (runtime ACLs), request/response header rules, HSTS, response compression (3.4 `filter comp-res`), custom error pages, HTTP/3 (QUIC) when the image reports QUIC support, backend TLS re‑encryption to servers, OIDC SSO for the panel.

---

## 20. Backlog of useful extras

- **Config import** from an existing `haproxy.cfg`: parse frontends/backends/servers that map onto the Loxy model, show what can't be imported (kept out, listed), create the objects in one transaction, then let Loxy own the config.
- **Maintenance page** per frontend with custom text (instant, runtime map).
- **Graceful drain** with configurable timeout, and **slow start** (`slowstart`) for servers returning to service.
- **Live logs**: HAProxy logs to `127.0.0.1:5514` UDP → loxy ring buffer per frontend, sampling (e.g. 1:10), off by default (privacy and performance first).
- **Config preview & diff** before applying risky changes; "Download HAProxy config".
- **Per‑server graphs** and response‑time / status‑code breakdowns.
- **Bandwidth limits** per frontend using HAProxy's `bwlim-in`/`bwlim-out` filters.
- **Duplicate frontend/backend**, **Export/Import JSON**, Terraform provider later (the API is resource‑shaped, which makes this easy).
- **Webhooks** for every event kind.
- **Rate limiting & IP lists** via stick tables and runtime ACLs (Phase 4).
- **HTTP/3** advertisement (`alt-svc`) when QUIC is available.
- **Runtime backends** (3.4 `add backend` / `publish backend`) to make some backend changes reload‑free.
- **Own authoritative DNS server** — explicitly *not* planned (non‑goal); revisit only if operators need GeoDNS/health‑based DNS failover. Today Loxy manages records at external providers (§8.9).
- **More DNS providers** via libdns (deSEC, Route 53, DigitalOcean, OVH, …) — one registry entry each.
- **Health‑check templates** using the 3.4 `healthcheck` section (reusable checks shared by backends).

---

## 21. Open questions for the owner (ask before Phase 1 ends; defaults in brackets)

1. Accent colour and logo [neutral red, simple wordmark "Loxy"]. (Name is decided: **Loxy**.)
2. Will people other than you log in (multi‑tenant projects)? [single owner + team, one project]
3. Is HA in scope for the first production rollout? [no — Phase 3, generic VRRP]
4. DNS providers for DNS‑01 and record management [libdns providers: Hetzner DNS, Cloudflare first, §8.9].

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
