# Loxy

A lightweight, Hetzner-style managed load balancer panel on top of
[HAProxy 3.4 LTS](https://www.haproxy.org/).

Create load balancers, services (HTTP, HTTPS, TCP), targets (servers, label
selectors, IPs) and certificates (uploaded or Let's Encrypt) from a small web
UI or REST API. Loxy renders and validates the HAProxy config, applies changes
through the runtime API when possible and hitless reloads otherwise, and keeps
metric history for graphs. One Go binary, SQLite, no other services.

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
and prints the panel URL. Pin a version with `LOXY_VERSION=vX.Y.Z`.

### Docker Compose by hand

```sh
sudo mkdir -p /opt/loxy/{data/haproxy-conf,data/haproxy-state,data/loxy,run/haproxy}
sudo chown -R 1500:1500 /opt/loxy/data /opt/loxy/run
cd /opt/loxy
sudo curl -fsSLO https://raw.githubusercontent.com/2shiraz/loxy/main/deploy/compose/compose.yml
sudo curl -fsSL  https://raw.githubusercontent.com/2shiraz/loxy/main/.env.example -o .env
# edit .env: set LOXY_VERSION and LOXY_SECRET_KEY=$(openssl rand -base64 32)
sudo chmod 0600 .env
sudo docker compose up -d
```

Open `https://<host>:8443` and log in with the one-time owner password:

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
make dev               # haproxy + 3 whoami targets + Pebble ACME + loxy (dev mode)
make gen               # OpenAPI -> Go + TS, sqlc
make lint              # gofmt, go vet, golangci-lint, svelte-check
make test              # unit tests
make test-integration  # against the real pinned HAProxy image (Docker)
make web               # build the SPA and stage it for embedding
make build             # bin/loxy
make image             # ghcr.io/2shiraz/loxy:<version>-haproxy<haproxy-version>
```

In dev the whoami targets listen on `127.0.0.1:9001-9003`, Pebble on
`https://127.0.0.1:14000/dir`; state is kept in `.dev/`.

Before contributing, read [INSTRUCTIONS.md](INSTRUCTIONS.md) (the source of
truth) and [CLAUDE.md](CLAUDE.md). Architecture decisions live in
[docs/adr/](docs/adr/), verified HAProxy behaviour in
[docs/haproxy-notes.md](docs/haproxy-notes.md).

## License

TBD.
