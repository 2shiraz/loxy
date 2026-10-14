# syntax=docker/dockerfile:1
ARG HAPROXY_VERSION=3.4.6
ARG GO_VERSION=1.26
ARG NODE_VERSION=24

# ---- web: SvelteKit SPA ----
FROM node:${NODE_VERSION}-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
COPY api/openapi.yaml /src/api/openapi.yaml
RUN npm run build          # emits build/ with .br and .gz siblings

# ---- go: static loxy binary with the SPA embedded ----
FROM golang:${GO_VERSION}-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN find internal/webui/dist -mindepth 1 ! -name .gitkeep -exec rm -rf {} + || true
COPY --from=web /src/web/build ./internal/webui/dist
ARG LOXY_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${LOXY_VERSION}" -o /out/loxy ./cmd/loxy

# ---- final: same HAProxy binary as production, so `haproxy -c` matches ----
FROM haproxy:${HAPROXY_VERSION}-alpine
USER root
RUN addgroup -g 1500 loxy && adduser -D -H -u 1500 -G loxy loxy \
 && mkdir -p /var/lib/loxy /usr/local/etc/haproxy /var/lib/haproxy /run/haproxy \
 && chown 1500:1500 /var/lib/loxy /usr/local/etc/haproxy /var/lib/haproxy /run/haproxy
COPY --from=go /out/loxy /usr/local/bin/loxy
USER 1500:1500
# wget (busybox) is used by the compose healthcheck against /healthz.
HEALTHCHECK --interval=5s --timeout=2s --retries=12 CMD wget -qO- http://127.0.0.1:9090/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/loxy"]
CMD ["all-in-one"]
