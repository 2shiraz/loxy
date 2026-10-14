# Loxy

A lightweight, self-hosted management panel for
[HAProxy 3.4 LTS](https://www.haproxy.org/) — a safer alternative to
hand-editing `haproxy.cfg`.

Manage HAProxy's own building blocks from a small web UI or REST API:

- **Frontends** — HTTP or TCP listeners on any addresses/ports (`*` = all),
  TLS termination with certificates, HTTP→HTTPS redirect, a default backend
  and host/path routing rules, timeouts, `maxconn`, forwarded headers,
  one-click maintenance mode.
- **Backends** — algorithm, health checks (TCP/HTTP/HTTPS), sticky sessions,
  PROXY protocol, timeouts; shared by as many frontends as you like.
- **Servers** — IPs, hosts from the inventory or label selectors; weight,
  backup, drain/maintenance and graceful removal, applied at runtime.
- **Hosts**, **certificates** (uploaded or Let's Encrypt via HTTP-01/DNS-01)
  and **domains & DNS** (required records, live verification, DNS providers
  via libdns).
- **Observability** — per-server health, live and historical graphs per
  frontend and backend, live logs, activity and audit log.

Every change is rendered, validated with `haproxy -c`, stored as a numbered
config revision (diff and rollback), and applied through the runtime API when
possible or a hitless reload otherwise. One Go binary, SQLite, no other
services.

> Status: under active development. The build spec is
> [INSTRUCTIONS.md](INSTRUCTIONS.md).

## Requirements

- Linux x86_64 or arm64, kernel >= 5.4
- Docker Engine >= 24 with Compose v2
- 1 vCPU / 1 GB RAM minimum

## Install

### Installer (recommended)

```sh
curl -fsSL https://raw.githubusercontent.com/2shiraz/loxy/main/deploy/install.sh | sudo bash
```

It checks prerequisites, applies host sysctls, creates `/opt/loxy`, writes
`compose.yml` and `.env` (with a generated `LOXY_SECRET_KEY`), starts the stack
and prints the panel URL. It installs the latest published GitHub release.
If no public release exists yet, it downloads the source and builds the image
on your server using Docker (no host Go or Node installation needed; the build
can take several minutes and needs more memory than running Loxy).

Pin a published version by passing it to the installer process:

```sh
curl -fsSL https://raw.githubusercontent.com/2shiraz/loxy/main/deploy/install.sh -o /tmp/loxy-install.sh
sudo env LOXY_VERSION=vX.Y.Z bash /tmp/loxy-install.sh
```

To explicitly build from source, use `sudo env LOXY_BUILD_FROM_SOURCE=1 bash
/tmp/loxy-install.sh`. `LOXY_REF` selects a source tag or commit (default `main`).
When run from a checkout, the installer builds that checkout.

### Docker Compose by hand

```sh
sudo mkdir -p /opt/loxy/{data/haproxy-conf,data/haproxy-state,data/loxy,run/haproxy}
sudo chown -R 1500:1500 /opt/loxy/data /opt/loxy/run
cd /opt/loxy
sudo curl -fsSLO https://raw.githubusercontent.com/2shiraz/loxy/main/deploy/compose/compose.yml
sudo curl -fsSL  https://raw.githubusercontent.com/2shiraz/loxy/main/.env.example -o .env
# Set LOXY_VERSION to an ACTUAL published release tag (not an empty value).
# Set LOXY_SECRET_KEY to the output of: openssl rand -base64 32
# If no release exists, use the source-build method below instead.
sudo chmod 0600 .env
sudo chmod 0770 run/haproxy
sudo docker compose config --quiet
sudo docker compose pull
sudo docker compose up -d
```

### Build from source without a published release

On your Linux server with Docker and Git installed:

```sh
git clone https://github.com/2shiraz/loxy.git loxy-src
cd loxy-src
sudo env LOXY_BUILD_FROM_SOURCE=1 bash deploy/install.sh
```

This builds the production image locally and installs it into `/opt/loxy`.
Re-running it preserves the existing `.env` settings and data. Never replace
an existing secret key when upgrading.

Open `https://<host>:8443` and log in as `admin@loxy.local` (override with
`LOXY_OWNER_EMAIL`) with the one-time owner password:

```sh
docker compose logs loxy | grep 'initial password'
```

Restrict panel access with `LOXY_PANEL_ALLOW` or a firewall. Never expose
ports 9090, 8405 or 5514. See [docs/operations.md](docs/operations.md) for
upgrades, backup/restore and break-glass procedures.

## Configuration

All settings are environment variables in `.env`; see
[.env.example](.env.example) for the full list with defaults.

## Development

Prerequisites: Go (see `go.mod`), Node 22+, Docker.

```sh
make dev               # haproxy + 3 whoami servers + Pebble ACME + loxy (dev mode)
make gen               # OpenAPI -> Go + TS, sqlc
make lint              # gofmt, go vet, golangci-lint, svelte-check
make test              # unit tests
make test-integration  # against the real pinned HAProxy image (Docker)
make web               # build the SPA and stage it for embedding
make build             # bin/loxy
make image             # ghcr.io/2shiraz/loxy:<version>-haproxy<haproxy-version>
```

In dev the whoami servers listen on `127.0.0.1:9001-9003` (loopback servers are
accepted only when `LOXY_DEV=true`), Pebble on `https://127.0.0.1:14000/dir`;
state is kept in `.dev/`.

Other useful commands: `loxy render --dry-run` (print the config that would be
applied), `loxy admin reset-password`, `loxy backup`, `make e2e` (Playwright
against a running instance, see `test/e2e/playwright.config.ts`).

Before contributing, read [INSTRUCTIONS.md](INSTRUCTIONS.md) (the source of
truth) and [CLAUDE.md](CLAUDE.md). Architecture decisions live in
[docs/adr/](docs/adr/), verified HAProxy behaviour in
[docs/haproxy-notes.md](docs/haproxy-notes.md).

## License

TBD.
