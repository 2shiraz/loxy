#!/usr/bin/env bash
# Loxy installer (INSTRUCTIONS.md §16.5).
#   curl -fsSL https://raw.githubusercontent.com/2shiraz/loxy/main/deploy/install.sh | sudo bash
# Environment overrides:
#   LOXY_VERSION     loxy release to install (default: latest GitHub release)
#   HAPROXY_VERSION  pinned HAProxy version (default: 3.4.6)
#   LOXY_DIR         install dir (default: /opt/loxy)
#   LOXY_PANEL_PORT  panel port (default: 8443)
#   LOXY_CHECK_PORTS extra space-separated ports to check are free (e.g. "80 443")
set -euo pipefail

REPO="2shiraz/loxy"
RAW="https://raw.githubusercontent.com/${REPO}/main"
LOXY_DIR="${LOXY_DIR:-/opt/loxy}"
HAPROXY_VERSION="${HAPROXY_VERSION:-3.4.6}"
LOXY_PANEL_PORT="${LOXY_PANEL_PORT:-8443}"
LOXY_CHECK_PORTS="${LOXY_CHECK_PORTS:-80 443}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo "")"

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarn:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# fetch <repo-relative path> <dest>: copy from a local checkout if present, else download.
fetch() {
  local rel="$1" dest="$2"
  if [[ -n "$SCRIPT_DIR" && -f "$SCRIPT_DIR/../$rel" ]]; then
    cp "$SCRIPT_DIR/../$rel" "$dest"
  elif [[ -f "./$rel" ]]; then
    cp "./$rel" "$dest"
  else
    curl -fsSL "$RAW/$rel" -o "$dest"
  fi
}

# ---- 1. checks ---------------------------------------------------------------
[[ $EUID -eq 0 ]] || die "run as root (sudo)"
command -v docker >/dev/null || die "docker not found; install Docker Engine >= 24"
command -v curl   >/dev/null || die "curl not found"
command -v openssl >/dev/null || die "openssl not found"

docker_major="$(docker version --format '{{.Server.Version}}' 2>/dev/null | cut -d. -f1 || true)"
[[ "$docker_major" =~ ^[0-9]+$ ]] || die "cannot talk to the Docker daemon"
(( docker_major >= 24 )) || die "Docker Engine >= 24 required (found $docker_major)"
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 plugin required ('docker compose')"

port_busy() {
  if command -v ss >/dev/null; then ss -Hltn "sport = :$1" 2>/dev/null | grep -q .
  else return 1; fi
}
if [[ -f "$LOXY_DIR/compose.yml" ]] && docker compose -f "$LOXY_DIR/compose.yml" ps -q 2>/dev/null | grep -q .; then
  log "existing installation in $LOXY_DIR is running; skipping port checks"
else
  for p in "$LOXY_PANEL_PORT" 9090 $LOXY_CHECK_PORTS; do
    port_busy "$p" && die "port $p is already in use"
  done
fi

if [[ -z "${LOXY_VERSION:-}" ]]; then
  LOXY_VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1 || true)"
  [[ -n "$LOXY_VERSION" ]] || die "could not determine latest release; set LOXY_VERSION"
fi
log "installing loxy $LOXY_VERSION with HAProxy $HAPROXY_VERSION into $LOXY_DIR"

# ---- 2. sysctl ---------------------------------------------------------------
fetch deploy/sysctl/99-loxy.conf /etc/sysctl.d/99-loxy.conf
sysctl --system >/dev/null || warn "sysctl --system reported errors (some keys may be unsupported in this kernel/container)"

# ---- 3. directories ----------------------------------------------------------
mkdir -p "$LOXY_DIR"/{data/haproxy-conf,data/haproxy-state,data/loxy,run/haproxy}
chown -R 1500:1500 "$LOXY_DIR/data" "$LOXY_DIR/run"
chmod 0750 "$LOXY_DIR"/data "$LOXY_DIR"/data/{haproxy-conf,haproxy-state,loxy}
chmod 0770 "$LOXY_DIR/run/haproxy"

# ---- 4. compose.yml + .env ---------------------------------------------------
fetch deploy/compose/compose.yml "$LOXY_DIR/compose.yml"

if [[ -f "$LOXY_DIR/.env" ]]; then
  log "keeping existing $LOXY_DIR/.env (updating versions only)"
  sed -i -e "s/^LOXY_VERSION=.*/LOXY_VERSION=${LOXY_VERSION}/" \
         -e "s/^HAPROXY_VERSION=.*/HAPROXY_VERSION=${HAPROXY_VERSION}/" "$LOXY_DIR/.env"
else
  umask 077
  cat > "$LOXY_DIR/.env" <<ENV
HAPROXY_VERSION=${HAPROXY_VERSION}
LOXY_VERSION=${LOXY_VERSION}
LOXY_SECRET_KEY=$(openssl rand -base64 32)
LOXY_LISTEN=127.0.0.1:9090
LOXY_PANEL_PORT=${LOXY_PANEL_PORT}
LOXY_PANEL_DOMAIN=
LOXY_PANEL_ALLOW=
LOXY_ACME_EMAIL=
LOXY_ACME_DIRECTORY=https://acme-v02.api.letsencrypt.org/directory
LOXY_DATA_DIR=/var/lib/loxy
LOXY_HAPROXY_CONF_DIR=/usr/local/etc/haproxy
LOXY_HAPROXY_RUN_DIR=/run/haproxy
LOXY_HAPROXY_BIN=haproxy
LOXY_STATS_INTERVAL=1s
LOXY_LOG_LEVEL=info
LOXY_DEV=false
ENV
  umask 022
fi
chmod 0600 "$LOXY_DIR/.env"

# ---- 5. start ----------------------------------------------------------------
cd "$LOXY_DIR"
log "pulling images"
docker compose pull
log "starting"
docker compose up -d

log "waiting for loxy to become healthy"
cid="$(docker compose ps -q loxy)"
for _ in $(seq 1 60); do
  status="$(docker inspect -f '{{.State.Health.Status}}' "$cid" 2>/dev/null || echo unknown)"
  [[ "$status" == healthy ]] && break
  sleep 2
done
[[ "${status:-}" == healthy ]] || die "loxy did not become healthy; see: cd $LOXY_DIR && docker compose logs loxy"

# ---- 6. done -----------------------------------------------------------------
host="$(hostname -I 2>/dev/null | awk '{print $1}')"
cat <<MSG

Loxy is running.

  Panel:            https://${host:-<this-host>}:${LOXY_PANEL_PORT}
  Initial password: cd ${LOXY_DIR} && docker compose logs loxy | grep 'initial password'

Restrict panel access: set LOXY_PANEL_ALLOW in ${LOXY_DIR}/.env and/or firewall
port ${LOXY_PANEL_PORT} to admin IPs. Never expose 9090, 8405 or 5514.
MSG
