# Loxy — a lightweight, Hetzner‑style managed Load Balancer on HAProxy

**Build instructions for the implementing agent**

| | |
|---|---|
| Name | **Loxy** — repo `github.com/2shiraz/loxy`, Go module `github.com/2shiraz/loxy`, binary `loxy`, image `ghcr.io/2shiraz/loxy`, env prefix `LOXY_`, DB file `loxy.db` |
| Data plane | **HAProxy 3.4 LTS** (official Docker image `haproxy:3.4.x-alpine`, patch version pinned) |
| Control plane | Single Go binary with embedded web UI, SQLite, no other services |
| UX reference | The Hetzner Cloud Load Balancer console (Overview · Graphs · Targets · Services · Networking · Rescale · Delete). Copy the *information architecture and behaviour*, not the brand (no Hetzner name, logo or exact colours). |
| Spec date | 2026‑10‑14 |

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
A self‑hosted control panel + REST API that turns one or more Linux servers running HAProxy into a "managed load balancer" product, with the same mental model as Hetzner Cloud Load Balancers:

- An operator creates **Load Balancers** (LBs). Each LB has its own public IPv4/IPv6 (and optionally a private IP), a **plan** (limits), an **algorithm**, **labels** and **delete protection**.
- Each LB has **Services** (a listener: protocol `tcp | http | https`, listen port → destination port) with health checks, sticky sessions, PROXY protocol, HTTP→HTTPS redirect and TLS certificates.
- Each LB has **Targets** (backend servers): a server from the inventory, a **label selector** that matches many servers, or a raw **IP**.
- The panel shows **health** per target/service, **live and historical graphs** (open connections, connections/s, HTTP requests/s, bandwidth in/out), usage vs plan limits and monthly traffic.
- Every change is applied to HAProxy automatically, validated, versioned, and **without dropping traffic** (runtime API where possible, hitless reload otherwise).

### 1.2 Goals
1. **Feature parity with the Hetzner LB console** (see §8) plus the extras in §8 and §20.
2. **Minimal install:** `docker compose up -d` with two containers (official HAProxy + `loxy`). No external database, queue, Prometheus or Redis required.
3. **Fast and light:** budgets in §1.4 are acceptance criteria.
4. **Safe:** invalid config can never reach the live path; every applied state is a numbered revision that can be rolled back.
5. **API‑first:** the UI uses only the public REST API, so everything is scriptable.

### 1.3 Non‑goals (v1)
Managing NGINX/Apache, Kubernetes ingress, a WAF rule engine, anycast/multi‑region traffic steering, billing/invoicing, UDP load balancing, **running an authoritative DNS server** (Loxy manages records at external DNS providers through their APIs, §8.16; it never answers DNS queries itself).

### 1.4 Performance budgets (acceptance criteria)

| Item | Budget |
|---|---|
| Controller resident memory, idle, 50 LBs / 200 services / 1 000 targets | ≤ 60 MB |
| Controller CPU, idle (1 s stats polling) | < 1 % of one core |
| API read p95 (served from in‑memory snapshot) | < 10 ms |
| Write that is applied via Runtime API (e.g. add target), request → active in HAProxy | < 200 ms |
| Write that needs a reload (validate + swap + reload), 200 services | < 1.5 s |
| UI initial load, all JS + CSS gzip | ≤ 150 KB (charts lazy‑loaded on the Graphs tab) |
| Live graph latency (counter change → pixel) | ≤ 2 s |
| Failed client requests during 100 consecutive reloads under a 2 000 rps load test | **0** |
| Failed client requests while adding/removing 50 targets under load | **0** |

---

## 2. Concept mapping (Hetzner → loxy → HAProxy)

| Hetzner concept | loxy object | HAProxy construct |
|---|---|---|
| Load Balancer (ID, name, IPv4, IPv6, network zone) | `load_balancers` row placed on a **node** (or HA node group) and owning IP addresses from the node's pool | All `frontend`/`backend` sections prefixed `lb<ID>_`, binding on the LB's IPs |
| LB type (LB11/LB21/LB31…) | `plans` (operator‑editable): max connections, services, targets, certificates, monthly traffic, price | Frontend `maxconn`; optional LB‑wide connection cap via a shared stick table; quotas enforced in the API |
| Service (protocol, listen → destination port) | `services` row | One `frontend lb<L>_s<S>_fe` + one `backend lb<L>_s<S>_be` (+ `frontend lb<L>_s<S>_rd` for the :80 redirect) |
| Target: server / label selector / IP | `targets` row, resolved to concrete servers | `server` lines in **every** service backend of that LB; added/removed at runtime as dynamic servers |
| Health check (protocol, port, interval, timeout, retries, domain, path, response, status codes, TLS) | columns on `services` | `option httpchk`, `http-check send/expect`, `check port inter rise fall`, `timeout check`, `check-ssl` |
| Algorithm Round Robin / Least Connections | `load_balancers.algorithm` | `balance roundrobin` / `balance leastconn` — **always explicit** (§7.9) |
| Sticky sessions (cookie name, lifetime) | service columns | `cookie <name> insert indirect nocache httponly [secure] maxlife <n>s` + `cookie <id>` per server |
| Proxy protocol | service column | `send-proxy-v2` **and** `check-send-proxy` on server lines |
| HTTP‑Redirect (301) | service column (https only) | Extra :80 frontend with `http-request redirect scheme https code 301` |
| HTTP timeout | service column | `timeout client` / `timeout server` (idle timeouts) on that service |
| Certificates (uploaded / managed Let's Encrypt) | `certificates` (project‑level, reusable) | PEM files + one `crt-list` per service; hot‑swapped through the Runtime API |
| Public interface on/off | `load_balancers.public_enabled` | Bind (or not) on the LB's public IPs |
| Private network attach | `networks`, `lb_networks` | Bind on the node's private IP; targets reached via their private IP |
| Labels | JSON column on LBs, servers, certificates | Used by label‑selector targets and list filters only |
| Delete protection (lock icon) | `load_balancers.delete_protected` | API refuses DELETE |
| Metrics (open connections, connections/s, requests/s, bandwidth in/out) | metrics collector + rollup tables | Deltas of `show stat` counters |
| Location / network zone | node metadata | — |

The Hetzner public API exposes exactly these four metric types (`open_connections`, `connections_per_second`, `requests_per_second`, `bandwidth` with `in`/`out` series); mirror them in the loxy API so existing tooling habits transfer.

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
          Clients ⇄ LB IP:port ⇄ HAProxy ⇄ Targets (public or private IPs)
```

### 3.2 Process modes (one binary)
`loxy all-in-one` (default; panel + local node agent in one process), `loxy panel` (central control plane, Phase 3), `loxy agent` (runs next to HAProxy on each LB node, Phase 3), plus admin sub‑commands (`loxy admin reset-password`, `loxy backup`, `loxy restore`, `loxy migrate`, `loxy render --dry-run`, `loxy version`).

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
7. On any failure the previous good revision stays (or is put back) as `live`, the LB shows an `apply_error` banner with the HAProxy output, and a notification fires.

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
| ACME | `github.com/go-acme/lego/v5` used as a library | HTTP‑01 and DNS‑01 (many DNS providers incl. Hetzner), custom challenge providers, renewal info (ARI) |
| DNS providers | `github.com/libdns/libdns` interfaces + provider modules (`libdns/hetzner`, `libdns/cloudflare` first) | One small, uniform record API (`GetRecords`/`AppendRecords`/`SetRecords`/`DeleteRecords`, `ListZones`) for many providers; the same credentials drive ACME DNS‑01 through a lego `challenge.Provider` adapter (§8.16) |
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
│   ├── state/                       # immutable NodeState snapshots, label-selector resolution
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
│   ├── dns/                         # libdns provider registry, lego DNS-01 adapter, domain verifier, record sync (§8.16)
│   ├── api/                         # handler implementations, middleware (auth, CSRF, rate-limit, request-id)
│   ├── auth/                        # sessions, argon2id, API tokens, RBAC, TOTP
│   ├── events/                      # in-process pub/sub + SSE hub
│   ├── audit/
│   ├── notify/                      # webhook, email (SMTP), Telegram, Slack
│   ├── logrecv/                     # UDP syslog receiver + per-LB ring buffers (live logs)
│   ├── agent/                       # node-side applier + NodeTransport (in-process now, network in Phase 3)
│   ├── cluster/                     # panel-side node registry, enrollment, transport server (Phase 3)
│   ├── integrations/hetzner/        # Robot + Cloud API clients: inventory sync, failover IP switch (Phase 3)
│   └── webui/                       # go:embed of web/build (precompressed .br/.gz)
├── web/                             # SvelteKit SPA (§13)
│   ├── src/lib/api/                 # generated types + thin client
│   ├── src/lib/components/          # StatBar, StatCard, Donut, Chart, Tabs, Table, Modal, Dropdown, Toast, LabelChips, PortField, ...
│   ├── src/lib/stores/              # session, sse, snapshot cache
│   ├── src/lib/styles/tokens.css
│   └── src/routes/                  # /login, /, /load-balancers/[id]/{overview,graphs,targets,services,networking,rescale,delete}, /certificates, /servers, /networks, /nodes, /activity, /settings/*
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
    ├── adr/                         # architecture decision records
    ├── haproxy-notes.md             # verified behaviours, version quirks
    └── operations.md                # backup, restore, upgrade, break-glass
```

---

## 6. Data model (SQLite)

Conventions: `INTEGER PRIMARY KEY` ids (shown in UI as `#1996290`), timestamps as unix milliseconds, JSON stored as `TEXT CHECK(json_valid(col))`, `ON DELETE` rules explicit, `PRAGMA foreign_keys=ON`, `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`. All resources belong to a `project` (one default project in v1; keeps the door open for multi‑tenant use).

| Table | Key columns |
|---|---|
| `projects` | id, name, created_at |
| `users` | id, email (unique), password_hash (argon2id), role (`owner|admin|operator|viewer`), totp_secret_enc, disabled, created_at, last_login_at |
| `project_members` | project_id, user_id, role |
| `sessions` | id_hash, user_id, created_at, expires_at, ip, user_agent |
| `api_tokens` | id, project_id, name, prefix, secret_hash, role, expires_at, last_used_at |
| `nodes` | id, name, mode (`local|remote`), status, haproxy_version, loxy_version, features JSON (`QUIC`, `PROMEX`, …), location_city, location_country, network_zone, lat, lon, last_seen_at, desired_version, applied_version |
| `node_groups` | id, name, kind (`single|ha_pair`), vip_mode (`none|hetzner_failover_ip|hcloud_floating_ip|vrrp`) — Phase 3 |
| `ip_addresses` | id, node_id / node_group_id, address, family (4/6), kind (`primary|additional|failover|private`), network_id, assigned_lb_id (nullable, unique when set), rdns |
| `networks` | id, project_id, name, cidr |
| `node_networks` | node_id, network_id, interface, address |
| `plans` | id, name (e.g. `lb11`), max_connections, max_services, max_targets, max_certificates, traffic_quota_bytes, price_monthly_cents, currency, sort, active |
| `load_balancers` | id, project_id, name (unique per project), plan_id, node_id / node_group_id, algorithm (`roundrobin|leastconn|source|random`), public_enabled, ipv4_id, ipv6_id, delete_protected, maintenance, labels JSON, status (`active|applying|apply_error`), last_error, created_at, updated_at |
| `lb_networks` | lb_id, network_id, private_ip_id |
| `services` | id, lb_id, protocol (`tcp|http|https`), listen_port, destination_port, http_redirect, sticky_enabled, sticky_cookie_name, sticky_cookie_lifetime_s, proxy_protocol (`none|v1|v2`), http_timeout_s, hc_protocol (`tcp|http|https`), hc_port, hc_interval_s, hc_timeout_s, hc_retries, hc_domain, hc_path, hc_response, hc_status_codes JSON, hc_tls_verify, options JSON (extras, §20), created_at — UNIQUE(lb_id, listen_port) |
| `service_certificates` | service_id, certificate_id, position |
| `servers` (inventory) | id, project_id, name, public_ipv4, public_ipv6, private_ipv4, labels JSON, source (`manual|hetzner_robot|hetzner_cloud`), external_id, notes |
| `targets` | id, lb_id, type (`server|label_selector|ip`), server_id, label_selector, ip, use_private_ip, weight (1–256, default 100), backup, admin_state (`ready|drain|maint`), created_at |
| `server_slots` | id (stable HAProxy server `id`), lb_id, target_id, server_id NULL, address — one row per *resolved* backend server, so ids survive reloads |
| `certificates` | id, project_id, name, type (`uploaded|managed`), domains JSON, key_type (`ecdsa256|rsa2048|rsa4096`), challenge (`http01|dns01`), dns_provider_id (→ `dns_providers`, replaces inline credentials), cert_pem, chain_pem, key_pem_enc, fingerprint_sha256, not_before, not_after, status (`pending|issued|renewing|failed`), last_error, renew_after, labels JSON |
| `dns_providers` | id, project_id, name, kind (`hetzner|cloudflare|…`, registry key), credentials_enc (AES‑256‑GCM JSON), status (`ok|error|unknown`), last_error, last_checked_at, created_at |
| `domains` | id, project_id, fqdn (unique per project, RFC 1123, lowercase, `*.` allowed only with a DNS provider), lb_id (nullable, `ON DELETE SET NULL`), service_id (nullable), dns_provider_id (nullable), zone (detected apex, e.g. `example.com`), auto_records (bool, default false), ttl (default 300), status (`unknown|ok|wrong_ip|missing|error`), observed JSON (last A/AAAA answers), checked_at, certificate_id (nullable), managed_record_ids JSON, created_at, updated_at |
| `acme_accounts` | id, directory_url, email, key_pem_enc, registration JSON, eab_kid, eab_hmac_enc |
| `config_revisions` | id, node_id, revision (monotonic), desired_version, sha256, kind (`runtime|reload|rollback|bootstrap`), result (`ok|failed`), duration_ms, haproxy_output, created_by, created_at, pinned |
| `metrics_1m`, `metrics_5m`, `metrics_1h` | lb_id, service_id (0 = whole LB), ts, open_conn_avg, open_conn_max, cps, rps, bytes_in, bytes_out, resp_time_ms_avg, http_2xx, http_3xx, http_4xx, http_5xx, errors — PK(lb_id, service_id, ts) |
| `traffic_monthly` | lb_id, month (`YYYY-MM`), bytes_in, bytes_out |
| `events` | id, ts, project_id, lb_id, kind, severity, message, data JSON |
| `audit_log` | id, ts, actor_user_id, actor_token_id, ip, action, resource_type, resource_id, before JSON, after JSON |
| `notification_channels` | id, kind (`webhook|email|telegram|slack`), config_enc, enabled |
| `alert_rules` | id, event_kinds JSON, lb_filter (label selector), channel_id, min_severity, cooldown_s |
| `settings` | key, value JSON |

Indexes on every foreign key, on `events(ts)`, `audit_log(ts)`, metrics PKs. Keep the whole desired state (minus metrics/events/audit) in an in‑memory snapshot rebuilt on change; API reads hit the snapshot, not SQLite.

---

## 7. HAProxy integration (the core)

### 7.1 Process model, sockets, container
- HAProxy runs in its **own container** from the **official image**, `network_mode: host` (services add listeners at runtime, which Docker port publishing cannot follow, and host networking avoids the userland proxy/NAT hop).
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
        ├── certs/_placeholder_<svc>.pem   # self-signed, used until a managed cert is issued
        ├── crt-lists/s<service_id>.lst
        ├── maps/maint.map            # loaded with opt@, updated at runtime too
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
- Frontend `lb{L}_s{S}_fe`; redirect frontend `lb{L}_s{S}_rd`; backend `lb{L}_s{S}_be`; per‑LB connection table `lb{L}_cap`; system sections `sys_panel`, `sys_panel_be`, `sys_stats`, `sys_acme`.
- Frontend and backend names MUST differ (HAProxy 3.3+ rejects a frontend and backend sharing a name).
- Server names: `t{T}` for an IP target, `t{T}x{SRV}` for a server or label‑selector expansion. Server `id` = `server_slots.id` (stable), so `load-server-state-from-file` re‑applies weight/admin/health state after reloads.
- Names in HAProxy use ids only; display names and labels never appear in the config.

### 7.4 Rendered config (reference output)

Templates live in `internal/haproxy/render/templates/`. Output MUST be deterministic (sorted by id) so revisions diff cleanly. Reference rendering for one LB with an HTTPS service (redirect on) and a TCP service:

```haproxy
# Generated by loxy. DO NOT EDIT. node=1 revision=123 desired_version=456
global
    master-worker
    user haproxy
    group haproxy
    stats socket /run/haproxy/admin.sock mode 660 gid 1500 level admin expose-fd listeners
    stats timeout 30s
    stats calculate-max-counters off          # 3.4: loxy computes maxima itself
    log 127.0.0.1:5514 len 8192 format rfc5424 local0 info
    maxconn 200000
    hard-stop-after 15m                       # bounds old workers holding long connections
    mworker-max-reloads 50
    server-state-file /var/lib/haproxy/server-state
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

# ---------- LB 7 (plan lb11) ----------
backend lb7_cap
    stick-table type integer size 1 expire 24h store conn_cur

frontend lb7_s31_fe from loxy_http
    bind 162.55.154.22:443 ssl crt-list /usr/local/etc/haproxy/live/crt-lists/s31.lst alpn h2,http/1.1
    bind ipv6@2a01:4f8:1c1f:696b::1:443 ssl crt-list /usr/local/etc/haproxy/live/crt-lists/s31.lst alpn h2,http/1.1
    maxconn 10000
    tcp-request connection track-sc0 int(1) table lb7_cap
    tcp-request connection reject if { sc0_conn_cur gt 10000 }
    timeout client 50s
    option forwardfor
    http-request set-header X-Forwarded-Proto https
    http-request set-header X-Forwarded-Port %[dst_port]
    http-request return status 503 content-type text/html file /usr/local/etc/haproxy/live/errors/maint.http if { str(7),map(opt@/usr/local/etc/haproxy/live/maps/maint.map) -m found }
    default_backend lb7_s31_be

frontend lb7_s31_rd from loxy_http
    bind 162.55.154.22:80
    bind ipv6@2a01:4f8:1c1f:696b::1:80
    http-request return status 200 content-type text/plain lf-string "%[path,field(-1,/),map(virt@acme_http01)]" if { path_beg /.well-known/acme-challenge/ } { path,field(-1,/),map(virt@acme_http01) -m found }
    http-request redirect scheme https code 301

backend lb7_s31_be from loxy_http
    balance roundrobin
    timeout server 50s
    option httpchk
    http-check send meth GET uri /health ver HTTP/1.1 hdr Host app.example.com
    http-check expect status 200-399
    http-check expect string 'OK'               # VERIFY AND-semantics, see §7.5
    timeout check 13s
    server t101 85.10.206.38:9000 id 101 check inter 15s rise 3 fall 3
    server t102 85.10.206.221:9000 id 102 check inter 15s rise 3 fall 3

frontend lb7_s32_fe from loxy_tcp
    bind 162.55.154.22:8443
    maxconn 10000
    tcp-request connection track-sc0 int(1) table lb7_cap
    tcp-request connection reject if { sc0_conn_cur gt 10000 }
    default_backend lb7_s32_be

backend lb7_s32_be from loxy_tcp
    balance leastconn
    server t101 85.10.206.38:443 id 1101 check inter 15s rise 3 fall 3 send-proxy-v2 check-send-proxy
```

Notes:
- The LB‑wide cap (`lb{L}_cap` + `track-sc0`) enforces the plan's connection limit across *all* services of the LB, like a Hetzner LB type. It is a setting (`plans.enforce_lb_cap`, default on). **VERIFY** that concurrent connections across two frontends share the counter.
- IPv6 binds use the `ipv6@addr:port` prefix form (unambiguous).
- The ACME and redirect rules are rendered in **every** :80 frontend of an LB that has a managed HTTP‑01 certificate. If an LB has a managed certificate but no :80 listener, render a `sys_acme` frontend on that LB's IPs:80 that answers challenges and returns 404 otherwise.
- `maint.map` is an `opt@` map: present on disk in the revision **and** updated at runtime with `add map`/`del map`, so maintenance mode is instant and survives reloads. `virt@acme_http01` is a virtual (memory‑only) map because challenge tokens are ephemeral; the reconciler re‑pushes pending tokens after every reload.

### 7.5 Field → directive mapping

**Service level**

| Field | HTTP / HTTPS | TCP |
|---|---|---|
| protocol `http` | `mode http`, plain bind | — |
| protocol `https` | `bind … ssl crt-list s<S>.lst alpn h2,http/1.1`; backend talks plain HTTP to targets (TLS termination) | — |
| protocol `tcp` | — | `mode tcp`; no header injection; use for TLS passthrough |
| listen_port / destination_port | bind port / server port | same |
| http_redirect (https only) | extra frontend on :80 with `http-request redirect scheme https code 301` | not allowed |
| sticky (cookie name, lifetime) | `cookie <name> insert indirect nocache httponly maxlife <n>s` (+ `secure` for https), each server `cookie t<id>` | not allowed |
| proxy_protocol v1/v2 | `send-proxy` / `send-proxy-v2` **and** `check-send-proxy` on every server line (otherwise targets that require PROXY reject the health check) | same |
| http_timeout_s | `timeout client` on the frontend, `timeout server` on the backend | same |
| forwarded headers | `option forwardfor`, `X-Forwarded-Proto`, `X-Forwarded-Port` = listen port (Hetzner behaviour) | none |
| algorithm (LB level) | `balance roundrobin` / `leastconn` (extras: `source`, `random`) — rendered **explicitly** | same |

**Health check**

| Field | Rendering |
|---|---|
| hc_protocol `tcp` | `check` only (L4 connect) |
| hc_protocol `http` | `option httpchk` + `http-check send meth GET uri <path>` (+ `ver HTTP/1.1 hdr Host <domain>` when domain set; without a domain HAProxy sends HTTP/1.0 without Host) |
| hc_protocol `https` | as `http` plus `check-ssl verify none` on servers; if `hc_tls_verify` → `check-ssl verify required ca-file @system-ca check-sni <domain>` (**VERIFY** `@system-ca` support in the image) |
| hc_port | `check port <p>` (omit when equal to destination port) |
| hc_interval_s | `inter <n>s` |
| hc_timeout_s | `timeout check <n>s` in the backend (must be < interval) |
| hc_retries | `fall <n> rise <n>` (unhealthy after n failures, healthy again after n successes) |
| hc_status_codes | `http-check expect status <list>` — HAProxy accepts comma‑separated codes and ranges (e.g. `200-399`) |
| hc_response | `http-check expect string '<s>'` |

Status‑code conversion (pure function, table‑tested): each item matches `^[1-5][0-9?]{2}$`; `2??` → `200-299`, `20?` → `200-209`, `204` → `204`; merge adjacent ranges (`2??,3??` → `200-399`); de‑duplicate; sort.

**VERIFY (important):** that two consecutive `http-check expect` rules after one `send` are both evaluated against the same response (status AND body). Test: target returns 200 without the string → must be DOWN; returns 500 with the string → must be DOWN; 200 with string → UP. If HAProxy evaluates only one, fall back to a raw `option tcp-check` sequence for that service (send an HTTP/1.1 request, `tcp-check expect rstring ^HTTP/1\.[01] (2|3)[0-9][0-9]`, then `tcp-check expect string <s>`), and document it in `docs/haproxy-notes.md`.

### 7.6 Change classification (runtime vs reload)

| Change | Path | Commands / notes |
|---|---|---|
| Add target (any type) when algorithm ∈ {roundrobin, leastconn, random} | runtime | `add server lb7_s31_be/t105 1.2.3.4:9000 id 105 check inter 15s rise 3 fall 3 [cookie t105] [send-proxy-v2 check-send-proxy]` → `enable server …` → `enable health …` (dynamic servers start in maintenance with checks off; `default-server` settings are **not** inherited, so render every keyword explicitly; the `enabled` keyword is rejected) |
| Remove target | runtime | `set server <b>/<s> state drain` → wait for `scur=0` or drain timeout (default 30 s) → `set server … state maint` → `wait 5s srv-removable <b>/<s>; del server <b>/<s>` |
| Weight change | runtime | `set server <b>/<s> weight <n>` |
| Drain / maintenance / ready | runtime | `set server <b>/<s> state drain|maint|ready` |
| Label‑selector match set changes (server labels edited) | runtime | treated as add/remove |
| Algorithm `source` (hash) backends | **reload** for target changes | dynamic servers only work with roundrobin, leastconn, first, random |
| Algorithm change | reload | |
| Add / edit / delete service | reload | new listeners need a new worker |
| Health‑check parameter change | reload | |
| Certificate renewed (same name) | runtime | `set ssl cert <path> <<\n<pem>\n` → `commit ssl cert <path>` |
| Certificate attached to a service | runtime | `new ssl cert <path>` → `set ssl cert …` → `commit ssl cert …` → `add ssl crt-list <lst> <<\n<path> [alpn h2,http/1.1] <sni…>\n` |
| Certificate detached | runtime | `del ssl crt-list <lst> <path>`; `del ssl cert <path>` when unused (**VERIFY** command availability) |
| ACME HTTP‑01 token | runtime | `add map virt@acme_http01 <token> <keyauth>` / `del map …` |
| Maintenance mode on/off | runtime | `add map /usr/local/etc/haproxy/live/maps/maint.map <lbid> 1` / `del map … <lbid>` (+ file in revision). The `opt@` prefix is not part of a map's name, so runtime commands use the plain path; `virt@` *is* part of the name |
| Panel allowlist | reload (rare) | MAY use `add acl` at runtime |
| Public interface toggle, network attach, IP change, rescale | reload | |

Every runtime op also produces the new on‑disk revision. If any runtime command fails, the reconciler falls back to a reload of that revision.

HAProxy 3.4 also allows creating, publishing and deleting whole **backends** at runtime (`add backend … from <defaults>`, `publish backend`, `unpublish backend`, `del backend`). v1 doesn't need it (services add listeners, which still require a reload); use it in Phase 4 for host/path routing rules (§20).

### 7.7 Reload procedure
1. `show servers state` → write `/var/lib/haproxy/server-state` atomically.
2. `haproxy -c -f revs/N/haproxy.check.cfg` (inside the loxy container, same HAProxy binary). Non‑zero exit → abort, keep current `live`, record the output.
3. Swap `live` → `revs/N`.
4. Master CLI `reload`; read until EOF; require `Success=1`.
5. On `Success=0`: swap `live` back to the previous revision (so a future cold start is safe), record the startup log, mark the LB(s) `apply_error`, notify. Old workers keep serving the previous config.
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

Global navigation: **Load Balancers · Domains · Certificates · Servers · Networks · Nodes · Activity · Settings** (Domains has sub‑pages *Domains*, *DNS providers*, *Zones*). Top bar: project switcher (hidden while only one project exists), user menu, live connection indicator (SSE state).

All destructive actions use a confirm modal; delete of an LB requires typing its name. Every mutation returns an **action** object (§12.4) whose progress is shown as a toast and in Activity.

### 8.1 Load Balancers list
- Table/cards: status dot, name, `#id`, IPv4, IPv6, plan, location (flag + city), services count, targets healthy/total, live open connections sparkline (last 5 min), labels.
- Filters: text search, label selector (`env=prod,team!=ops`), status. Sort by name/created/connections.
- "Create Load Balancer" button → wizard (§8.2).
- Acceptance: 100 LBs render in < 100 ms after data arrives; sparklines update via one SSE stream, not per‑row polling.

### 8.2 Create wizard (single page with sections, like Hetzner)
1. **Location** – choose node / HA group (shows city, country, network zone, free IPs).
2. **Type** – plan cards with limits and price.
3. **Network** – public IPv4/IPv6 (auto‑assign free address from the node's pool or pick), optional private network + IP.
4. **Algorithm** – Round Robin (default) / Least Connections.
5. **Services** – optional; add from templates (§8.7).
6. **Targets** – optional; same pickers as §8.6.
7. **Name & labels**.
- Creates everything in one transaction → one reload.
- Acceptance: create with one HTTP service + 2 IP targets → reachable, both targets healthy, within 3 s of submit on a dev box.

### 8.3 LB header (all tabs)
- Plan badge (`LB11`), editable name (inline, underline‑on‑hover), status triangle, `#id`, IPv4, IPv6 (copy on click), network zone.
- **Lock icon** toggles delete protection (confirm when disabling).
- **Actions** menu: Rename · Edit labels · Enable/disable delete protection · Maintenance mode on/off · Download HAProxy config (read‑only, only this LB's sections) · Export JSON · Clone · Copy ID.
- Apply‑error banner with the HAProxy output and "Retry" / "Roll back to last good revision" buttons.
- Tabs: **Overview · Graphs · Targets · Services · Networking · Rescale · Delete** (+ **Logs** when live logs are enabled, §20).

### 8.4 Overview tab
**Usage bar** (live via SSE):

| Card | Value |
|---|---|
| Targets | resolved targets / plan.max_targets |
| Services | services / plan.max_services |
| Certificates | distinct certificates used by this LB's services / plan.max_certificates |
| Connections | current open connections / plan.max_connections (show limit when 0 usage, like Hetzner) |
| Traffic out | month‑to‑date bytes out / plan quota, human units (TB) |
| Price | plan price per month (hidden if plan has no price) |

**Labels** row: chips + "Add labels" (key/value editor, §14.4 rules).

**Health status** card: donut of healthy checks / total checks (check = service × resolved target); badge `Healthy | Degraded | Unhealthy | Unknown`; message ("All services are healthy." or the first 3 failing checks with reason, e.g. "85.10.206.38 → :443 · Layer7 wrong status 503").

**Location** card: city, country (flag), network zone; small static SVG map with a pin from node lat/lon (lazy‑loaded asset, ≤ 30 KB, no tile servers).

**Options** card: algorithm dropdown (applies immediately after confirm), public network Enabled/Disabled (links to Networking), "Change settings" → modal with LB‑level settings (algorithm, drain timeout, maintenance page text).

### 8.5 Graphs tab
- Range selector: **Live** (rolling 5 min, 1 s points, auto‑updating), 30 min, 1 h, 6 h, 24 h, 7 d, 30 d.
- Charts (same order as Hetzner): **Open connections**, **Connections per second**, **HTTP requests per second** (hidden if the LB has no HTTP/HTTPS services), **Bandwidth** (in = red, out = yellow; units auto‑scale B/s → GB/s).
- Extra "Details" section (collapsed by default): average response time (ms), HTTP responses per second by class (2xx/3xx/4xx/5xx), errors/s (connection errors, response errors, denied), queued requests. Filter dropdown: All services / one service / one target.
- Behaviour: uPlot with synced cursors across charts; tooltip with exact values and timestamp in the browser's timezone; Live mode pauses while hovering; no false spikes or dips across reloads (§10.3).
- Acceptance: switching ranges renders in < 150 ms for 30 d; Live mode updates every second with ≤ 2 s lag; CPU in the browser < 5 % on a laptop.

### 8.6 Targets tab
- **Add targets** dropdown:
  - **Server** — multi‑select from inventory with search and label filter; "Use private IP" toggle (only when the LB is attached to a network).
  - **Label selector** — input with syntax `key=value`, `key!=value`, `key in (a,b)`, `key` (exists), comma = AND; live preview "matches N servers".
  - **IP** — one or many (paste, one per line); IPv4/IPv6; rejects loopback, link‑local, multicast and the LB's own IPs.
- **Grouped / List** toggle. Grouped: each label selector is a group (tag icon, "N Servers", aggregate health, ⋯); server targets grouped as "Servers"; IP targets grouped as "IP targets". List: flat table.
- Row columns: Name (or "—" for IPs), Public IP, Private IP (if used), **Status** = healthy services / total services for this target (green `1/1`, amber partial, red `0/n`, grey unknown), Weight, ⋯ menu: Drain · Maintenance · Enable · Set weight · Show in graphs · Remove.
- Removal is graceful (drain → maint → delete, §7.6) and shows progress.
- Quota enforcement: adding beyond `max_targets` (counting resolved servers) returns `422 quota_exceeded`.
- Empty state: "No targets added yet." (per group as in Hetzner).
- Acceptance: adding 25 IP targets to an LB with 5 services results in 125 runtime `add server` ops, **no reload**, all active in < 1 s, zero failed requests under load.

### 8.7 Services tab
- Intro text, **Add service** dropdown: **New Service**; Templates: **HTTP** (http 80→80), **TLS Termination** (https 443→80, redirect on, certificate required), **TLS Passthrough** (tcp 443→443).
- Service card header: protocol + `:listen → :dest`, shield icon when certificates attached, health summary line (`Interval 15 s | Timeout 10 s | Retries 3 | Domain - | Path / | Response - | Status codes 2??, 3??`), ⋯ menu: Edit · Duplicate · Delete.
- Card body: per‑target status table (Name, Public IP, Status badge with tooltip `L7OK/200 in 3 ms` or failure reason).
- **Edit form** (inline card like Hetzner): Protocol select; Source port; Destination port; HTTP‑Redirect (301) checkbox (https only); Certificates chips + "Add certificates" picker (existing certs or create managed/upload inline); summary block of Health check / HTTP timeout / Sticky sessions / Proxy protocol with a pencil → **Advanced settings** modal:
  - Health check: protocol (tcp/http/https), destination port, interval (s), timeout (s), retries, domain, path, response, status codes, TLS verify (https checks).
  - HTTP timeout (s).
  - Sticky sessions: checkbox + cookie name + lifetime (s).
  - Proxy protocol: checkbox + version, with a red warning: enabling it for targets that don't accept PROXY makes the whole service unreachable.
- Defaults: hc interval 15 s, timeout 10 s, retries 3, path `/`, status codes `2??, 3??`; HTTP timeout 60 s; sticky cookie `LBSTICKY`, lifetime 300 s; health‑check port = destination port.
- Validation (server side, mirrored client side):
  - `listen_port` 1–65535, unique per LB, and not colliding with another LB on the same IP, with the redirect's :80, or with reserved system ports (panel port, 8405 on loopback, 5514).
  - `https` requires ≥ 1 certificate (a pending managed cert is allowed; a placeholder self‑signed cert is served until issuance).
  - `http_redirect` only for https and only when :80 is free on the LB.
  - Sticky only for http/https.
  - `hc_timeout < hc_interval`; interval 3–3600; timeout 1–3600; retries 1–10.
  - http/https checks require a path; tcp ignores HTTP fields (returned as null).
  - Quotas: services ≤ `max_services`, distinct certificates ≤ `max_certificates`.
- Acceptance: creating the TLS Termination template with a managed cert on a domain pointing to the LB yields a valid Let's Encrypt (Pebble in CI) certificate and a 301 from :80 within 60 s, with only one reload.

### 8.8 Networking tab
- **Public network** card: IPv4 and IPv6 with copy buttons, reverse DNS (editable when a Hetzner integration is configured, Phase 3), toggle **Enable/Disable public interface** (confirm; requires at least one private network attached).
- **Private networks** card: attached networks (name, CIDR, private IP, node interface); **Attach to network** (choose a network the node is connected to; auto or chosen IP); **Detach** (blocked while targets use private IPs through it).
- **Additional IPs** (extra): assign more addresses from the node pool; services bind on all LB IPs.
- All changes are reload‑class.

### 8.9 Rescale tab
- Plan cards with limits and price; current plan highlighted.
- A plan is disabled with a reason when current usage exceeds its limits ("You use 7 services; LB11 allows 5").
- Applying changes quotas, `maxconn` and the LB cap → one reload; traffic counters unaffected.

### 8.10 Delete tab
- Text like Hetzner: deleting removes services and target assignments; targets and certificates are untouched.
- Button disabled while delete protection is on (inline link "Disable protection").
- Confirm modal: type the LB name. Effect: remove its sections (reload), release IPs, delete rows, keep metrics for 7 days then purge, audit entry.

### 8.11 Certificates page (project level)
- List: name, type (Uploaded/Managed), domains, status (Pending/Issued/Renewing/Failed), expires in (amber < 21 d, red < 7 d), used by N services, labels.
- **Upload**: paste or file for certificate, chain, private key. Validate: key matches cert, not expired, chain completes to a root in the system bundle (warn only), reorder chain if needed. Key stored encrypted at rest.
- **Create managed**: domains (≤ 100), challenge HTTP‑01 (default; domains must resolve to an LB that will serve the challenge) or DNS‑01 (pick a connected DNS provider from §8.16 — credentials are not entered again; required for wildcards), key type ECDSA P‑256 (default) / RSA 2048/4096.
- Detail drawer: SANs, issuer, serial, fingerprint, validity, last issuance log, renewal schedule. Actions: Renew now · Download chain (never the key unless owner role + re‑auth) · Delete (blocked while in use).

### 8.12 Servers (inventory)
- CRUD of servers: name, public IPv4/IPv6, private IPv4, labels, notes. Bulk import from CSV.
- Shows where each server is used (LBs/services) and its current health across them.
- Phase 3: sync from Hetzner Robot (dedicated) and Hetzner Cloud API (cloud servers, labels) — read‑only mirror with "source" badge.

### 8.13 Networks and Nodes
- Networks: name + CIDR; per node the interface and address.
- Nodes (all‑in‑one shows the local node): status, HAProxy version and feature flags (`haproxy -vv`: QUIC, PROMEX, OpenSSL version), loxy version, uptime, reload count, CPU/memory, connections; editable location (city, country, network zone, lat/lon) and IP pool (public, additional, failover, private addresses available to LBs). Phase 3 adds enrollment and HA groups (§15).

### 8.14 Activity
- **Events** feed (health transitions, reload results, cert issued/failed/expiring, quota warnings, node offline) with filters by LB/kind/severity.
- **Audit log** (who, what, when, before/after diff, IP, token) — read‑only, exportable CSV.
- **Config revisions** per node: list, diff between any two, "Roll back to this revision" (re‑applies the *desired state* stored with it, not just the text), pin/unpin.

### 8.15 Settings
Users & roles · API tokens · Plans editor · ACME (account email, directory: Let's Encrypt production / staging / custom, EAB) · Notification channels & alert rules · Panel (domain, port, allowlist, session lifetime, 2FA enforcement) · System (hard‑stop‑after, drain timeout, reload min interval, metrics retention, live‑log sampling) · Backup (schedule, retention, optional S3‑compatible target).

### 8.16 Domains & DNS

Loxy helps operators point names at load balancers and keeps DNS in sync, **without running its own DNS server** (non‑goal, §1.3). All record changes go through the APIs of external DNS providers using the **libdns** ecosystem (`github.com/libdns/*`).

**Domains page** (`/domains`)
- List: FQDN, attached LB (and optional service), DNS provider (or "manual"), status badge (**Resolves correctly** / **Wrong IP** / **Missing** / **Unknown**), last checked, certificate (issued / pending / none), auto‑managed records on/off.
- **Add domain**: FQDN (validated per §14.4), choose LB (required) and optionally one service of that LB, optional DNS provider (auto‑detected when a connected provider owns a matching zone), "Manage A/AAAA records automatically" checkbox (only with a provider; Phase 2).
- Detail drawer: **Required records** table built from the LB's public IPs — `A <fqdn> → <ipv4>` and `AAAA <fqdn> → <ipv6>` (TTL 300) with copy buttons; **live verification**: Loxy resolves the name (A and AAAA, via the system resolver and, when a provider is attached, also by reading the provider's records) and shows per family: ✓ resolves correctly, ✗ wrong IP (shows the observed addresses), ✗ missing.
- Verification runs on add, on "Check now", every 5 minutes for unverified domains and every hour for verified ones, and whenever the LB's IPs change. Status changes emit `domain.status.changed` events.
- **Issue certificate** (one click) is enabled once the domain is verified: creates a managed certificate for the FQDN (HTTP‑01 if the name resolves to the LB, or DNS‑01 if a provider is attached — required for wildcards) and attaches it to the chosen https service, if any.

**DNS providers** (`/domains/providers`)
- Connect provider accounts: **Hetzner DNS** (`dns.hetzner.com` API token) and **Hetzner Console DNS** (Cloud API token, zones in the Hetzner Console), **Cloudflare** (scoped API token: `Zone:Read`, `DNS:Edit`). The registry is a map `kind → {fields schema, constructor}` so new libdns providers are added in one file.
- Credentials are encrypted at rest with AES‑256‑GCM (`LOXY_SECRET_KEY`, §14.5), never returned by the API (only `"configured": true`), and tested on save with a `ListZones` call (status `ok|error`).
- **ACME DNS‑01 reuses the same credentials**: `internal/dns` implements lego's `challenge.Provider` (`Present`/`CleanUp`) on top of the libdns `RecordAppender`/`RecordDeleter` interfaces, writing `_acme-challenge.<name> TXT <value>` (TTL 60) into the right zone and deleting exactly that record afterwards. Certificates reference a `dns_provider_id` instead of carrying their own credentials, so credentials live in one place.

**Auto‑managed records** (Phase 2)
- Opt‑in per domain. When on and a provider is connected, Loxy creates the A/AAAA records for the FQDN pointing at the LB's public IPs, **updates** them when the LB's IPs change (public interface toggle, IP reassignment, LB moved), and **removes** them when the domain is detached, deleted or the LB is deleted.
- Loxy only touches records it created (their provider IDs are tracked in `domains.managed_record_ids`); pre‑existing records with the same name are reported as a conflict and never overwritten without explicit confirmation.
- Sync runs through the reconciler's event stream (debounced) and is retried with backoff; failures surface as `domain.sync.failed` events.

**Zone editor** (Phase 2, `/domains/zones`)
- List zones from all connected providers; open a zone to list and edit records, similar to the Hetzner DNS console: types **A, AAAA, CNAME, TXT, MX, CAA, SRV, NS**; columns type, name (relative, `@` for apex), value, TTL; add / edit / delete with confirm.
- Server‑side validation per type: A/AAAA parsed with `net/netip`; CNAME/NS/MX targets are RFC 1123 hostnames; MX priority 0–65535; SRV `priority weight port target`; CAA `flags tag "value"` with tag ∈ `issue|issuewild|iodef`; TXT ≤ 255 chars per string (longer values are split); TTL 60–86400; CNAME cannot coexist with other records of the same name. Records managed by Loxy are marked and edits to them warn.

**API** (§12.2): `/domains`, `/domains/{id}/actions/{verify,issue_certificate}`, `/dns_providers`, `/dns_providers/{id}/zones`, `/dns_providers/{id}/zones/{zone}/records`.

**Acceptance**
- Adding a domain whose A record points to the LB shows "Resolves correctly" within one check; a wrong A record shows "Wrong IP" with the observed address.
- With a Hetzner or Cloudflare provider connected, a managed DNS‑01 certificate for `*.example.com` is issued (Pebble + a libdns test double in CI) without entering credentials again.
- With auto‑records on, changing the LB's IPv4 updates the A record within 10 s; deleting the domain removes exactly the records Loxy created.

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

Aggregation: target status = healthy services / total services; service status = healthy targets / total; LB status = `Healthy` if all checks healthy, `Degraded` if some unhealthy but every service has ≥ 1 healthy target, `Unhealthy` if any service has 0 healthy targets, `Unknown` without targets. Transitions produce `target.down` / `target.up` / `service.down` events (with check reason) used by notifications; debounce flapping with the alert‑rule cooldown. Optionally enable `option log-health-checks` and parse health transitions from the UDP log stream for sub‑second detection.

---

## 10. Metrics pipeline

### 10.1 Collection
Every 1 s per node: `show stat` (CSV; parse the header line to map columns by name, never by position) and `show info`. Frontend rows → LB/service metrics; server rows → per‑target metrics and health; backend rows → response times and queue. One `show stat` call returns everything; it is cheap even with thousands of servers.

### 10.2 Derived series (per service, summed per LB)

| Series | Derivation |
|---|---|
| open_connections | Σ frontend `scur` |
| connections_per_second | Δ frontend `conn_tot` / Δt (fallback `conn_rate`) |
| requests_per_second | Δ frontend `req_tot` / Δt (HTTP frontends only) |
| bandwidth_in / bandwidth_out | Δ frontend `bin` / Δt, Δ `bout` / Δt (bytes/s) |
| http_1xx…5xx per second | Δ frontend `hrsp_*` / Δt |
| response_time_ms | backend `rtime` (moving average HAProxy maintains), `ttime` |
| errors per second | Δ backend `econ` + `eresp`, frontend `ereq`, `dreq` |
| queue | backend `qcur` |
| traffic_out_month | Σ Δ `bout`, persisted in `traffic_monthly` |

### 10.3 Counter resets
New workers start counters at zero. Detect a new worker via `show info` `Pid`/`Uptime_sec` change or any counter decreasing; for that sample use `delta = current` (not negative, not a spike). Connections still served by old workers during drain are not counted — accepted. HAProxy 3.3+ has an experimental `shm-stats-file` that can preserve counters across reloads; MAY be enabled behind a setting after a **VERIFY** test.

### 10.4 Storage and retention
- In memory: ring buffer of 1 s points for the last 60 min per LB and per service (≈ 3 600 × ~12 series × 8 B ≈ 350 KB per service at worst; keep per‑target series only for the last 15 min).
- SQLite rollups written in one transaction per minute: `metrics_1m` (keep 7 d), `metrics_5m` (35 d), `metrics_1h` (400 d). Each rollup stores avg and max where meaningful.
- Range → resolution: Live/30 min → 1 s (downsample to ≤ 1 800 points), 1 h → 10 s (from memory), 6 h/24 h → 1 m, 7 d → 5 m, 30 d → 1 h.

### 10.5 API format
Columnar, uPlot‑native:
```json
{ "start": 1760412300, "end": 1760412600, "step": 1,
  "timestamps": [1760412300, 1760412301, ...],
  "series": { "open_connections": [2210, 2215, ...], "connections_per_second": [41, 18, ...],
              "requests_per_second": [8820, 8790, ...], "bandwidth.in": [4.1e6, ...], "bandwidth.out": [4.3e6, ...] } }
```
Live: `GET /api/v1/load_balancers/{id}/metrics/stream` (SSE) pushes one compact point per second.

### 10.6 Optional exports
- `sys_stats` frontend on `127.0.0.1:8405`: HAProxy's built‑in Prometheus exporter at `/metrics` (render only if `haproxy -vv` shows PROMEX) and the native stats page at `/stats`.
- loxy's own `/metrics` (controller health: reload durations, apply errors, poll latency, ACME results).

---

## 11. Certificates and ACME

### 11.1 Storage
Certificates are project‑level and reusable across services. Private keys are encrypted at rest (AES‑256‑GCM, key from `LOXY_SECRET_KEY`). The rendered revision necessarily contains plaintext PEMs (HAProxy reads them); the config volume is mode 0750, files 0640, group `loxy` (§16).

### 11.2 Managed certificates with lego
- One ACME account per directory URL (`acme_accounts`), created lazily with the configured email (and EAB when required by the CA).
- **HTTP‑01**: implement lego's `challenge.Provider` (`Present(ctx, domain, token, keyAuth)` / `CleanUp(...)`). `Present` pushes `add map virt@acme_http01 <token> <keyAuth>` to **every node** that hosts an LB serving that domain, waits for acknowledgement, then returns; `CleanUp` deletes the entry. HAProxy answers the challenge itself with the `http-request return … map(virt@acme_http01)` rule (§7.4) — no extra hop, no port juggling.
- **DNS‑01**: a lego `challenge.Provider` implemented on top of the libdns provider of a connected DNS provider account (§8.16), so DNS credentials are stored once (encrypted) and shared by record management and ACME; required for wildcard domains.
- Key type default ECDSA P‑256; RSA optional.
- Issuance runs as an async action; failures record the lego error and retry with backoff (1 m, 5 m, 30 m, 2 h, then every 6 h) without blocking the service (placeholder cert keeps serving).
- **Renewal** scheduler: honour ACME Renewal Information (ARI) when the CA offers it; otherwise renew at 2/3 of the lifetime (≈ 30 days before expiry for 90‑day certs). Jitter start times. On success: new revision with the new PEM + runtime `set ssl cert` / `commit ssl cert` (no reload).
- Use Let's Encrypt **staging** automatically in dev and CI (Pebble in tests).
- HAProxy's native ACME (`acme` section, 3.2+) MAY replace lego later once it's no longer experimental; keep the `CertIssuer` interface abstract so that's a swap, not a rewrite.

### 11.3 Uploaded certificates
Same runtime hot‑swap path when replaced. Expiry warnings at 21/7/1 days.

---

## 12. REST API

### 12.1 Conventions
- Base path `/api/v1`, JSON, `snake_case`, resource shapes deliberately similar to the Hetzner Cloud API (`load_balancers`, `services`, `targets`, `certificates`, `actions`, `metrics`) so it feels familiar.
- Auth: session cookie (UI) or `Authorization: Bearer loxy_<prefix>_<secret>` (API tokens).
- Pagination: `?page=&per_page=` (max 100) with `meta.pagination`. Filtering: `label_selector=`, `name=`. Sorting: `sort=name:asc`.
- Optimistic concurrency: every mutable resource has `version`; PUT/PATCH require `If-Match`; mismatch → `409 conflict`.
- Errors: `{ "error": { "code": "invalid_input", "message": "...", "details": { "fields": [{ "name": "listen_port", "messages": ["already in use"] }] } } }`. Codes: `invalid_input`, `quota_exceeded`, `conflict`, `protected`, `not_found`, `forbidden`, `unauthorized`, `rate_limited`, `apply_failed`.
- Idempotency: POSTs accept `Idempotency-Key`.

### 12.2 Endpoints

| Method & path | Purpose |
|---|---|
| `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, `POST /auth/totp/*` | Session auth |
| `GET/POST /load_balancers`, `GET/PATCH/DELETE /load_balancers/{id}` | LB CRUD (PATCH: name, labels, algorithm, delete_protected, maintenance) |
| `GET/POST /load_balancers/{id}/services`, `GET/PUT/DELETE /load_balancers/{id}/services/{sid}` | Services |
| `GET/POST /load_balancers/{id}/targets`, `PATCH/DELETE /load_balancers/{id}/targets/{tid}` | Targets (PATCH: weight, admin_state) |
| `POST /load_balancers/{id}/actions/change_plan` | Rescale |
| `POST /load_balancers/{id}/actions/{enable,disable}_public_interface` | Networking |
| `POST /load_balancers/{id}/actions/{attach_to,detach_from}_network` | Networking |
| `GET /load_balancers/{id}/status` | Health tree: services × targets with check details |
| `GET /load_balancers/{id}/metrics?type=open_connections,requests_per_second,bandwidth&start=&end=&step=` | History (§10.5) |
| `GET /load_balancers/{id}/metrics/stream` | SSE live points |
| `GET /load_balancers/{id}/config` | Rendered HAProxy sections for this LB (read‑only) |
| `GET /load_balancers/{id}/logs/stream` | SSE live logs (when enabled) |
| `GET/POST /certificates`, `GET/PATCH/DELETE /certificates/{id}`, `POST /certificates/{id}/actions/renew` | Certificates |
| `GET/POST /domains`, `GET/PATCH/DELETE /domains/{id}`, `POST /domains/{id}/actions/verify`, `POST /domains/{id}/actions/issue_certificate` | Domains (§8.16) |
| `GET/POST /dns_providers`, `GET/PATCH/DELETE /dns_providers/{id}`, `GET /dns_providers/{id}/zones`, `GET/POST /dns_providers/{id}/zones/{zone}/records`, `PUT/DELETE /dns_providers/{id}/zones/{zone}/records/{rid}` | DNS providers and zone editor (§8.16; records Phase 2) |
| `GET/POST /servers`, `GET/PATCH/DELETE /servers/{id}`, `POST /servers/import` | Inventory |
| `GET/POST /networks`, … | Networks |
| `GET /nodes`, `GET/PATCH /nodes/{id}`, `GET /nodes/{id}/revisions`, `GET /nodes/{id}/revisions/{rev}/diff?against=`, `POST /nodes/{id}/revisions/{rev}/actions/rollback` | Nodes & revisions |
| `GET /plans`, admin CRUD | Plans |
| `GET /actions`, `GET /actions/{id}` | Async action status |
| `GET /events` (SSE), `GET /events/history` | Event stream & history |
| `GET /audit` | Audit log |
| `GET/POST/DELETE /users`, `/api_tokens`, `/notification_channels`, `/alert_rules`, `GET/PATCH /settings` | Admin |
| `GET /healthz`, `GET /readyz`, `GET /metrics` | Ops (no auth on healthz; readyz checks DB + HAProxy socket) |

### 12.3 Example resources
```json
{
  "load_balancer": {
    "id": 1996290, "name": "load-balancer-1", "version": 14,
    "plan": { "name": "lb11", "max_connections": 10000, "max_services": 5, "max_targets": 25, "max_certificates": 10, "traffic_quota_bytes": 20000000000000 },
    "location": { "city": "Nuremberg", "country": "DE", "network_zone": "eu-central" },
    "public_net": { "enabled": true, "ipv4": "162.55.154.22", "ipv6": "2a01:4f8:1c1f:696b::1" },
    "private_net": [ { "network_id": 3, "ip": "10.0.0.5" } ],
    "algorithm": "roundrobin", "delete_protected": false, "maintenance": false,
    "labels": { "env": "prod" }, "status": "active",
    "usage": { "targets": 11, "services": 1, "certificates": 1, "open_connections": 2210, "traffic_out_month_bytes": 0 }
  }
}
```
```json
{
  "service": {
    "id": 31, "protocol": "https", "listen_port": 443, "destination_port": 9000,
    "http": { "redirect_http": true, "timeout_s": 50, "certificates": [12],
              "sticky_sessions": false, "cookie_name": "LBSTICKY", "cookie_lifetime_s": 300 },
    "proxy_protocol": "none",
    "health_check": { "protocol": "http", "port": 9000, "interval_s": 15, "timeout_s": 13, "retries": 3,
                      "http": { "domain": null, "path": "/", "response": "OK", "status_codes": ["2??", "3??"], "tls_verify": false } }
  }
}
```

### 12.4 Actions
Long or multi‑step operations return `202` with `{ "action": { "id", "command", "status": "running|success|error", "progress": 0-100, "resources": [...], "error": null, "started", "finished" } }`. The UI follows actions over the `/events` SSE stream (no polling).

### 12.5 SSE event kinds
`lb.updated`, `lb.apply.started|succeeded|failed`, `target.health.changed`, `service.health.changed`, `certificate.updated`, `domain.status.changed`, `domain.sync.failed`, `action.updated`, `node.status.changed`, `metrics.tick` (only on metric streams). Reconnect with `Last-Event-ID`; server keeps a 2‑minute replay buffer.

### 12.6 Codegen
`make gen` runs `oapi-codegen` (Go server interfaces + models) and `openapi-typescript` (web types). CI fails if generated files are stale.

---

## 13. Frontend implementation

- SvelteKit SPA (`adapter-static`, fallback `index.html`, `ssr = false`), served by loxy from `go:embed` with precompressed `.br`/`.gz` assets, `Cache-Control: public, max-age=31536000, immutable` for hashed files and `no-cache` for `index.html`.
- Routes mirror the tabs so every tab is linkable (`/load-balancers/1996290/services`).
- Data: one session‑scoped snapshot store fed by REST on navigation and kept fresh by the single `/events` SSE connection; mutations are optimistic where safe (labels, names) and action‑driven otherwise.
- Charts: `Chart.svelte` wraps uPlot, dynamically imported only on the Graphs tab and the Overview sparkline (sparklines use a 1 KB SVG polyline component, not uPlot).
- Components: `StatBar`, `StatCard`, `Donut`, `HealthBadge`, `Tabs`, `Table` (virtualised above 200 rows), `Modal`, `Dropdown`, `Toast`, `LabelChips`, `LabelSelectorInput`, `PortField`, `CertPicker`, `ServerPicker`, `CopyText`, `ConfirmTyped`, `CodeView` (read‑only config/diff viewer, no heavy editor).
- Design tokens in `tokens.css`; dark theme default (matches the reference screenshots), light theme via `prefers-color-scheme` + toggle; accent colour configurable in Settings (default not Hetzner's brand red):
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
Roles: **owner** (everything incl. users, secrets), **admin** (all resources, settings except owner transfer), **operator** (LBs, services, targets, certs; no users/settings), **viewer** (read‑only). API tokens carry a role and optional expiry; shown once at creation; stored as SHA‑256 hash with a visible prefix.

### 14.3 Web hardening
CSRF: SameSite=Strict cookies **and** a required `X-LOXY-CSRF` header token on mutating requests. Headers: strict CSP (`default-src 'self'`), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `Frame-Options: DENY`, HSTS on the panel. Request body limit 1 MB (5 MB for cert upload).

### 14.4 Input validation (config‑injection defence)
User values end up inside `haproxy.cfg`. **Never** interpolate raw text. Validate with strict allow‑lists, then render through a typed template layer that quotes per HAProxy rules (single quotes; reject values containing quotes).

| Field | Rule |
|---|---|
| Resource display names | 1–64 chars, any printable; **never rendered** into config |
| Label keys | `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?[a-zA-Z0-9]([-a-zA-Z0-9_.]{0,61}[a-zA-Z0-9])?$` |
| Label values | `^([a-zA-Z0-9]([-a-zA-Z0-9_.]{0,61}[a-zA-Z0-9])?)?$` |
| Domains (health check Host, cert SANs) | RFC 1123 hostname, lowercase, ≤ 253; `*.` prefix only for DNS‑01 certificates |
| Health check path | `^/[A-Za-z0-9._~!&()*+,;=:@%/?-]{0,254}$` (no spaces, quotes, `#`, `$`, `\`) |
| Health check response string | printable ASCII except `'"\#$`, 1–128 chars |
| Cookie name | `^[A-Za-z0-9_-]{1,64}$` |
| Ports | integers 1–65535 |
| IPs / CIDRs | parsed with `net/netip`; targets reject loopback, link‑local, multicast, unspecified |
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
- `loxy agent` runs next to HAProxy on each LB node (same compose as all‑in‑one minus the panel). It dials **out** to the panel over HTTPS WebSocket, so nodes need no inbound management port.
- Enrollment: panel creates a node and shows a one‑time join token; `loxy agent join --panel https://panel.example.com --token …` stores a long‑lived node credential and pins the panel's TLS certificate fingerprint.
- Messages: panel → agent `DesiredState{version, haproxy config inputs, certs, acme_tokens}`; agent → panel `Applied{version, result, output}`, `StatsBatch` (1 s samples, sent every 1 s), `HealthDelta`, `NodeInfo`.
- The agent persists the last applied desired state locally and keeps running autonomously if the panel is down (HAProxy keeps serving; ACME HTTP‑01 tokens pushed before disconnect stay valid).

### 15.2 HA pairs (active/passive)
Hetzner dedicated servers use **failover IPs** that are routed to one server's main IP via the Robot web service (`POST https://robot-ws.your-server.de/failover/<failover-ip>` with `active_server_ip=<node main IP>`); routing switches are reported to take on the order of tens of seconds. Hetzner Cloud servers use **Floating IPs** via the Cloud API.
- Both nodes of an HA group receive identical desired state; both bind the LB IPs (`net.ipv4.ip_nonlocal_bind=1` and `net.ipv6.ip_nonlocal_bind=1` on the host) and have the failover IPs configured on an interface.
- Leader election and health: keepalived (VRRP in **unicast** mode between the nodes' private IPs; don't rely on multicast in hosted networks) with a `track_script` that checks HAProxy (`/healthz` of the agent + a TCP probe of a service port). `notify_master` calls loxy (`loxy ha promote`) which switches every failover/floating IP of the group to itself via the Hetzner API (credentials from the panel, scoped to the node group).
- Stick tables (sticky/rate‑limit state) are synced with an HAProxy `peers` section between the two nodes.
- UI: node group page shows active/passive, last failover, "Fail over now" button.

### 15.3 Inventory sync (optional integration)
Hetzner Robot API (dedicated servers) and Hetzner Cloud API (cloud servers incl. labels) → `servers` table, read‑only mirror, every 5 min or on demand.

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
(If `nf_conntrack` is loaded, raise `net.netfilter.nf_conntrack_max` accordingly or exclude LB ports from tracking.)

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
1. Check root, Docker ≥ 24, Compose v2, free ports (8443 and the planned LB ports).
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
- **Firewall:** allow LB service ports publicly; restrict 8443 to admin IPs; never expose 9090, 8405 or 5514.

---

## 17. Development workflow

- `make dev` → `docker compose -f deploy/compose/compose.dev.yml up` with: HAProxy (same image), three `traefik/whoami` targets on different ports (one can be toggled unhealthy via an env flag), **Pebble** (Let's Encrypt's ACME test server) + its challenge DNS, and loxy in dev mode with live reload; Vite dev server proxies `/api` to loxy.
- `make gen` (OpenAPI → Go + TS, sqlc), `make lint` (golangci‑lint, `svelte-check`, eslint, prettier), `make test`, `make test-integration`, `make e2e`, `make load`, `make build`, `make image`.
- `make seed` creates a demo project: 2 LBs, services of each protocol, 6 targets, an uploaded cert, a managed cert against Pebble, and synthetic traffic (`oha` loop) so graphs have data.
- Conventional commits; CI on every PR: gen‑check, lint, unit, integration (real HAProxy via testcontainers), e2e (headless), bundle‑size check, image build.

---

## 18. Testing strategy

| Layer | What |
|---|---|
| Unit | Validation rules; status‑code conversion; label‑selector parser; plan quota math; metric delta/reset logic; health aggregation; reconciler classification table (§7.6) |
| Renderer golden tests | Fixture models → expected `haproxy.cfg` (byte‑exact); every golden passes `haproxy -c` in CI |
| Fuzz | Random valid models → config passes `haproxy -c`, no injected lines (§14.4) |
| Integration (testcontainers, real HAProxy 3.4) | Runtime client matchers for every command; reload success and failure paths (`Success=0` keeps old workers, symlink restored); server‑state survives reload; dynamic server add/remove/drain; cert hot‑swap; ACME HTTP‑01 + DNS‑01 against Pebble; metrics deltas across reloads; **all VERIFY items**: `-S` socket options, LB‑wide cap across frontends, double `http-check expect` semantics, `@system-ca`, `del ssl cert`, minimal capability set, `chroot auto`, prompt mode |
| E2E (Playwright) | Login, create LB via wizard, add service from each template, add targets of all three types, toggle sticky/proxy protocol, rescale up/down (blocked case), maintenance mode, delete protection, delete LB, certificate upload/managed flow, graphs render, rollback |
| Load / zero‑downtime | `oha` at 2 000 rps against an LB while (a) 100 reloads, (b) 50 target add/removes, (c) cert renewals — **0 non‑2xx/connection errors** allowed |
| Resource | loxy RSS and CPU under the §1.4 dataset; UI bundle size budget |

---

## 19. Roadmap and Definitions of Done

**Phase 0 — Foundations**
Repo skeleton, Makefile, CI, OpenAPI skeleton + codegen, SQLite + migrations, auth (owner login, sessions), runtime client + parsers, renderer + golden tests, revision FS + validator, bootstrap config, reload procedure.
*DoD:* `docker compose up` on a clean VM brings up HAProxy with the panel reachable at :8443; `loxy render --dry-run` works; reload path integration tests pass.

**Phase 1 — Hetzner parity on a single node (MVP)**
LB CRUD + wizard, plans/rescale, labels, delete protection, maintenance mode, services (tcp/http/https, redirect, sticky, proxy protocol, HTTP timeout, full health checks), targets (IP, server inventory, label selector) with runtime apply, certificates (upload + managed HTTP‑01/DNS‑01), Overview, Graphs (live + history), Targets, Services, Networking (public toggle, private network bind), Rescale, Delete, Activity (events + audit), Users/roles, **Domains & DNS part 1** (§8.16): domains page with required records and live verification, DNS provider accounts (Hetzner, Cloudflare) with encrypted credentials, DNS‑01 via the libdns‑backed lego provider, one‑click certificate issuance.
*DoD:* every acceptance criterion in §8 for these screens passes in E2E; zero‑downtime load tests pass; §1.4 budgets met.

**Phase 2 — Operations polish**
Config revision diff/rollback UI, notifications (webhook, email, Telegram, Slack) and alert rules, API tokens, drain/weights UI, live logs (sampled), Prometheus endpoints, backups (+ S3), 2FA, `loxy` CLI client for the API, **Domains & DNS part 2** (§8.16): auto‑managed A/AAAA records and the zone editor.
*DoD:* alert on target down arrives within 5 s; rollback restores previous behaviour verified by integration test; nightly backup + restore round‑trip test.

**Phase 3 — Multi‑node and HA**
`loxy panel` / `loxy agent`, enrollment, node pages, HA groups with keepalived + Hetzner failover IP / floating IP switching, `peers` sync, inventory sync from Hetzner APIs, rDNS editing.
*DoD:* killing the active node moves traffic to the passive node automatically; the panel going down doesn't affect traffic; agent reconnects and reconciles.

**Phase 4 — Advanced L7 (optional)**
Host/path routing rules to target groups (using 3.4 dynamic backends), per‑service IP allow/deny lists, rate limiting per client IP, custom request/response headers, HSTS, response compression (3.4 `filter comp-res`), custom error pages, HTTP/3 (QUIC) when the image reports QUIC support, re‑encryption to targets (backend TLS), OIDC SSO.

---

## 20. Backlog of useful extras (beyond Hetzner)

- **Maintenance mode** per LB with a custom page (instant, runtime map).
- **Graceful drain** with configurable timeout, and **slow start** (`slowstart`) for targets returning to service.
- **Weights** and **backup targets** (`backup` servers used only when all primaries are down).
- **Live logs** tab: HAProxy logs to `127.0.0.1:5514` UDP → loxy ring buffer per LB, sampling (e.g. 1:10), off by default (Hetzner keeps no request logs; privacy and performance first).
- **Config preview & diff** before applying risky changes; "Download HAProxy config".
- **Per‑target graphs** and response‑time / status‑code breakdowns.
- **Traffic quota actions**: alert at 80/100 %; optional bandwidth cap using HAProxy's `bwlim-out` filter.
- **Clone LB**, **Export/Import JSON**, Terraform provider later (API is Hetzner‑shaped, which makes this easy).
- **Webhooks** for every event kind.
- **Rate limiting & IP lists** via stick tables and runtime ACLs.
- **HTTP/3** advertisement (`alt-svc`) when QUIC is available.
- **Own authoritative DNS server** — explicitly *not* planned (non‑goal); revisit only if operators need GeoDNS/health‑based DNS failover. Today Loxy manages records at external providers (§8.16).
- **More DNS providers** via libdns (deSEC, Route 53, DigitalOcean, OVH, …) — one registry entry each.
- **Health‑check templates** using the 3.4 `healthcheck` section (reusable checks shared by services).

---

## 21. Open questions for the owner (ask before Phase 1 ends; defaults in brackets)

1. Accent colour and logo [neutral red, simple wordmark "Loxy"]. (Name is decided: **Loxy**.)
2. Will customers other than you log in (multi‑tenant projects, per‑project quotas)? [single owner + team, one project]
3. Which IP model per LB on your dedicated servers: one additional/failover IP per LB, or shared node IP with distinct ports? [one IP per LB from the node's pool; shared IP allowed with a warning]
4. Default plans and prices to seed [LB11 10 000 conns / 5 services / 25 targets / 10 certs / 20 TB; LB21 20 000 / 15 / 75 / 25; LB31 40 000 / 30 / 150 / 50 — limits mirror Hetzner's published types; prices empty].
5. DNS provider for DNS‑01 and record management [Hetzner DNS and Cloudflare via libdns, §8.16].
6. Is HA in scope for the first production rollout? [no — Phase 3]

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
- Hetzner Load Balancers overview & types — https://docs.hetzner.com/cloud/load-balancers/overview
- Hetzner Load Balancers FAQ (protocols, headers, PROXY protocol, health checks) — https://docs.hetzner.com/networking/load-balancers/faq/
- Hetzner Cloud API (load balancer metrics types) — https://docs.hetzner.cloud/
- Hetzner Robot web service (failover IP routing) — https://robot.your-server.de/doc/webservice/en.html
- libdns (provider‑agnostic DNS record APIs for Go) — https://github.com/libdns/libdns
- lego ACME library (custom challenge providers, DNS providers) — https://go-acme.github.io/lego/
- Roxy‑WI (for contrast) — https://github.com/roxy-wi/roxy-wi
