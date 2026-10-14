#!/usr/bin/env bash
# Update an existing standard /opt/loxy Compose install from source.
# LOXY_DIR selects the install; LOXY_REF selects a commit/tag/branch (default main).
set -euo pipefail
umask 077

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
log() { printf '==> %s\n' "$*"; }
[[ $EUID -eq 0 ]] || die "run as root (sudo)"
for tool in docker curl tar python3; do
  command -v "$tool" >/dev/null || die "$tool is required"
done
cd "${LOXY_DIR:-/opt/loxy}"
[[ -f compose.yml && -f .env && -f data/loxy/loxy.db ]] || die "expected an existing standard Loxy installation in $PWD"

# Read only the version; never execute .env as shell code.
haproxy_version="$(sed -n 's/^HAPROXY_VERSION=//p' .env | tr -d '\r\"\047')"
haproxy_version="${haproxy_version:-3.4.6}"
[[ "$haproxy_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "invalid HAPROXY_VERSION in .env"
unset LOXY_VERSION HAPROXY_VERSION # Compose must use the installation's .env.
docker compose config --quiet
cid="$(docker compose ps -q loxy)"
[[ -n "$cid" ]] || die "Loxy must be running to take an online backup"

# Refuse the destructive frontend/backend -> projects migration during updates.
python3 - <<'PY'
import sqlite3
with sqlite3.connect('file:data/loxy/loxy.db?mode=ro', uri=True) as db:
    found = db.execute("SELECT 1 FROM schema_migrations WHERE name = '0002_projects.sql'").fetchone()
    if not found:
        raise SystemExit('error: this install needs the projects migration, which removes frontend/backend definitions; migrate it separately before using this updater')
PY

work="$(mktemp -d)"
cleanup() { rm -rf "$work"; }
trap cleanup EXIT
ref="${LOXY_REF:-main}"
[[ "$ref" =~ ^[A-Za-z0-9_.-]+$ ]] || die "LOXY_REF must be a commit, tag, or simple branch name"
log "resolving $ref"
curl -fsSL "https://api.github.com/repos/2shiraz/loxy/commits/$ref" -o "$work/commit.json"
sha="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["sha"])' "$work/commit.json")"
[[ "$sha" =~ ^[a-f0-9]{40}$ ]] || die "GitHub returned an invalid commit"
version="update-${sha:0:12}"
image="ghcr.io/2shiraz/loxy:${version}-haproxy${haproxy_version}"
log "building $sha; existing traffic continues during the build"
curl -fsSL "https://api.github.com/repos/2shiraz/loxy/tarball/$sha" -o "$work/source.tar.gz"
mkdir "$work/src"
tar -xzf "$work/source.tar.gz" -C "$work/src" --strip-components=1
docker build -f "$work/src/deploy/docker/Dockerfile" \
  --build-arg "HAPROXY_VERSION=$haproxy_version" --build-arg "LOXY_VERSION=$sha" \
  -t "$image" "$work/src"

backup="backups/update-$(date -u +%Y%m%d-%H%M%S)-$$"
mkdir -p "$backup"
log "saving backups in $PWD/$backup"
cp -p .env compose.yml "$backup/"
old_image="$(docker inspect -f '{{.Image}}' "$cid")"
docker tag "$old_image" "loxy-rollback:${version}"
printf '%s\n' "loxy-rollback:${version}" > "$backup/image.txt"
docker compose exec -T loxy loxy backup > "$work/backup-path"
db_backup="$(tail -n1 "$work/backup-path")"
[[ "$db_backup" == /* ]] || die "backup did not return an absolute database path"
docker cp "$cid:$db_backup" "$backup/loxy.db"
if [[ -f data/loxy/secret.key ]]; then cp -p data/loxy/secret.key "$backup/"; fi
tar -czf "$backup/haproxy-files.tar.gz" data/haproxy-conf data/haproxy-state

# Validate the new Compose configuration before touching the installed .env.
LOXY_VERSION="$version" docker compose config --quiet
python3 - "$version" <<'PY'
import os, pathlib, sys
p = pathlib.Path('.env')
lines = p.read_text().splitlines()
lines = [line for line in lines if not line.startswith('LOXY_VERSION=')]
lines.append('LOXY_VERSION=' + sys.argv[1])
tmp = pathlib.Path('.env.update')
tmp.write_text('\n'.join(lines) + '\n')
tmp.chmod(0o600)
os.replace(tmp, p)
PY
log "replacing only the panel container"
if ! docker compose up -d --no-deps --pull never --wait --wait-timeout 120 loxy; then
  printf 'Update failed. Backups: %s/%s\nPrevious image: loxy-rollback:%s\n' "$PWD" "$backup" "$version" >&2
  printf 'Check docker compose logs loxy before rolling back; the database may have migrated.\n' >&2
  exit 1
fi
docker compose exec -T loxy loxy version
log "update complete; HAProxy container was left running. Backup: $PWD/$backup"
