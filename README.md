# Loxy

Simple, self-hosted load balancers on your own servers, powered by
[HAProxy 3.4 LTS](https://www.haproxy.org/). One Go binary, SQLite and the
official HAProxy image — no other services.

## How it works

A **project** is one load balancer. Create as many as you like. Each one has:

- **Targets** — where traffic goes: IP addresses (optionally `ip:port`), hosts
  from your inventory, or a label selector such as `role=web`. Set weights,
  mark backups, drain or pause a target; changes apply instantly without a
  reload.
- **Services** — the ports it listens on: `http`, `https` or `tcp`, listen
  port → destination port, health checks, sticky sessions, PROXY protocol.
  Every new project gets its own free port automatically (from 20000 up).
- **Domains** — attach `app.example.com` and Loxy routes it to the project on
  ports 80/443. HTTPS is automatic: as soon as the name points at your server,
  Loxy gets a Let's Encrypt certificate and redirects HTTP to HTTPS. A DNS
  provider (via libdns) can manage the records for you.

Plus live and historical graphs per project and service, live logs, health per
target, maintenance mode, certificates, users and roles, API tokens, 2FA,
notifications and backups. Every change is validated with `haproxy -c`, stored
as a config revision you can diff and roll back, and applied without dropping
traffic.

## Quick start

1. **Create a project** — give it a name. It is reachable right away on its own
   port, e.g. `http://<server-ip>:20001`.
2. **Add targets** — paste the IPs of your app servers (or pick hosts / a label
   selector). Loxy health-checks them and starts sending traffic.
3. **Reach it** — use the project's port, or attach a domain: create the A/AAAA
   records Loxy shows you, and HTTPS turns on by itself.

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

Restrict panel access with `LOXY_PANEL_ALLOW` or a firewall. Open the project
ports you use, plus 80/443 when you use domains. Never expose ports 9090, 8405
or 5514. See [docs/operations.md](docs/operations.md) for
upgrades, backup/restore and break-glass procedures.

## Update an existing installation

For the standard `/opt/loxy` Compose install already using Projects / Targets /
Services, update the panel with one command:

```sh
curl -fsSL https://raw.githubusercontent.com/2shiraz/loxy/main/deploy/update.sh | sudo bash
```

This resolves `main` to an exact commit and builds it on the server, keeping the
installed HAProxy version. It saves an online database backup, settings, secret
key (when stored in a file), config revisions and the previous image, then
replaces only the Loxy container and waits for it to become healthy. HAProxy's
container keeps running; Loxy validates and reloads the configuration as needed.
The panel is briefly unavailable. Docker, curl, tar and Python 3 are required.
Check CI for the selected commit before updating production.

To choose a specific commit or tag, or a different install directory:

```sh
curl -fsSL https://raw.githubusercontent.com/2shiraz/loxy/main/deploy/update.sh -o /tmp/loxy-update.sh
sudo env LOXY_REF=<commit-or-tag> LOXY_DIR=/opt/loxy bash /tmp/loxy-update.sh
```

The updater refuses the older Frontends / Backends database migration because
that migration removes definitions. It preserves `.env` settings apart from
`LOXY_VERSION` and does not overwrite `compose.yml` or apply host sysctls.
If startup fails, it reports the backup directory and saved image; database
migrations require review before rolling back.

## Configuration

All settings are environment variables in `.env`; see
[.env.example](.env.example) for the full list with defaults.

## Development

Prerequisites: Go (see `go.mod`), Node 22+, Docker.

```sh
make dev               # haproxy + 3 whoami targets + Pebble ACME + loxy (dev mode)
make gen               # OpenAPI -> Go + TS, sqlc
make lint              # gofmt, go vet, golangci-lint, svelte-check
make test              # unit tests
make test-integration  # against the real pinned HAProxy image (Docker)
make web               # build the SPA and stage it for embedding
make build             # bin/loxy
make image             # ghcr.io/2shiraz/loxy:<version>-haproxy<haproxy-version>
```

In dev the whoami targets listen on `127.0.0.1:9001-9003` (loopback targets are
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
