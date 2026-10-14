# Operations

Runbook for an all-in-one install in `/opt/loxy` (see INSTRUCTIONS.md §16.6).
All commands run from `/opt/loxy`.

## Layout

| Path | Contents |
|---|---|
| `compose.yml`, `.env` (0600) | Deployment and settings (`LOXY_SECRET_KEY` lives here — back it up separately) |
| `data/loxy/` | `loxy.db` (SQLite, WAL), backups |
| `data/haproxy-conf/` | Config revisions + `live` symlink |
| `data/haproxy-state/` | HAProxy server-state file |
| `run/haproxy/` | Stats and master sockets |

## Upgrade loxy

For an existing standard installation already using the projects model:

```sh
curl -fsSL https://raw.githubusercontent.com/2shiraz/loxy/main/deploy/update.sh | sudo bash
```

The updater builds the selected source commit (`LOXY_REF`, default `main`) with
the installed HAProxy version. Before replacing only Loxy, it saves an online
database copy, `.env`, `compose.yml`, the fallback `secret.key` if present, and
HAProxy config/state files under `backups/update-<timestamp>-<pid>/`. It tags the
previous image as `loxy-rollback:update-<commit>` and records that tag in
`image.txt`. It waits up to 120 seconds for container health. Check System for
config apply errors and verify production traffic after the update.

The panel briefly restarts; the HAProxy container stays running. The updater
requires Python 3 and refuses the destructive frontend/backend migration below.
It leaves the existing Compose file and host configuration intact. If the new
container fails, inspect its logs and the backup before rolling back: restoring
an image alone is insufficient if its database schema has changed.

For a published image release, use:

```sh
sed -i 's/^LOXY_VERSION=.*/LOXY_VERSION=vX.Y.Z/' .env
docker compose pull loxy && docker compose up -d --no-deps loxy
```

HAProxy keeps serving; no traffic impact. DB migrations run at start after an
automatic pre-migration backup in `data/loxy/backups/`.

Upgrading from the frontend/backend version runs migration `0002_projects`
(ADR 0006): frontend and backend definitions are dropped and the 1-minute
metrics are cleared, so recreate them as projects (services + targets); domains
are kept but detached — attach them to the new projects on the Domains page.
Hosts, certificates, users, API tokens, DNS providers, events and settings are
kept. Only a pre-release database with a `load_balancers` table is refused at
start with an error; start from an empty data directory for that one.

## Upgrade HAProxy

Bump `HAPROXY_VERSION` **and** `LOXY_VERSION` to a loxy image built for that
HAProxy version (tag `<loxy>-haproxy<haproxy>`), then:

```sh
docker compose pull && docker compose up -d
```

Replacing the HAProxy container restarts it (brief interruption). Schedule it,
or in an HA pair: fail over, upgrade the passive node, fail back. Loxy shows a
warning banner if its bundled `haproxy -v` differs from the running HAProxy.

## Backup

Nightly by default, 14 kept. Manual:

```sh
docker compose exec loxy loxy backup
```

A backup is the SQLite online copy (`VACUUM INTO`) plus a tarball of the live
config revision. Optional upload to S3-compatible storage (Settings). Encrypted
secrets in the DB are useless without `LOXY_SECRET_KEY` — store `.env` safely.

## Restore

```sh
docker compose exec loxy loxy restore /var/lib/loxy/backups/<file>
```

The reconciler then re-applies the restored state to HAProxy (validated with
`haproxy -c` before going live, like any change).

## Break-glass

| Situation | Command |
|---|---|
| Lost owner password | `docker compose exec loxy loxy admin reset-password` |
| See what would be applied | `docker compose exec loxy loxy render --dry-run` |
| Bad config is live | `docker compose exec loxy loxy admin rollback --revision N` |
| Initial password | `docker compose logs loxy \| grep 'initial password'` |
| Loxy down | HAProxy keeps serving the last live config; fix loxy, restart it |

## Firewall

Open the project ports you use (the service listen ports, 20000+ by default),
plus 80/443 when you use domains. Restrict the panel port (8443) to admin IPs
(`LOXY_PANEL_ALLOW` and/or host firewall). Never expose 9090 (internal API),
8405 (stats/metrics) or 5514 (syslog receiver).
