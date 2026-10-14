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

```sh
sed -i 's/^LOXY_VERSION=.*/LOXY_VERSION=vX.Y.Z/' .env
docker compose pull loxy && docker compose up -d loxy
```

HAProxy keeps serving; no traffic impact. DB migrations run at start after an
automatic pre-migration backup in `data/loxy/backups/`.

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

Allow LB service ports publicly. Restrict the panel port (8443) to admin IPs
(`LOXY_PANEL_ALLOW` and/or host firewall). Never expose 9090 (internal API),
8405 (stats/metrics) or 5514 (syslog receiver).
