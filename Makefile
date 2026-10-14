# Loxy — developer entry points (INSTRUCTIONS.md §17).
SHELL := /bin/bash

VERSION         ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)
HAPROXY_VERSION ?= 3.4.6
IMAGE           ?= ghcr.io/2shiraz/loxy
GO              ?= go
# GOLANGCI= (empty) skips golangci-lint, e.g. in CI where the action runs it.
GOLANGCI        ?= golangci-lint

.PHONY: all gen gen-check web build lint test test-integration dev image seed e2e load clean help

all: build

help:
	@grep -E '^[a-z-]+:' $(MAKEFILE_LIST) | cut -d: -f1 | sort | tr '\n' ' '; echo

## gen: regenerate sqlc, oapi-codegen (Go) and openapi-typescript (TS) output
gen:
	cd internal/store && sqlc generate
	oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
	cd web && npx openapi-typescript ../api/openapi.yaml -o src/lib/api/schema.d.ts

## gen-check: fail if generated code is out of date
gen-check: gen
	@git diff --exit-code || { echo "generated files are stale: run 'make gen' and commit the result" >&2; exit 1; }

## web: build the SPA and stage it for go:embed
web:
	cd web && npm ci && npm run build
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
	cp -R web/build/. internal/webui/dist/

## build: static loxy binary
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/loxy ./cmd/loxy

## lint: gofmt, go vet, golangci-lint (if installed), svelte-check (if web deps installed)
lint:
	@out="$$(gofmt -l ./cmd ./internal ./test 2>/dev/null)"; \
	  if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	@if [ -n "$(strip $(GOLANGCI))" ] && command -v $(GOLANGCI) >/dev/null 2>&1; then $(GOLANGCI) run; \
	  else echo "golangci-lint skipped"; fi
	@if [ -d web/node_modules ]; then cd web && npm run check; \
	  else echo "web/node_modules missing; skipping web checks"; fi

## test: unit tests (no -race: the build is CGO-free, -race needs CGO)
test:
	$(GO) test ./... -count=1

## test-integration: testcontainers-go against the pinned HAProxy image
test-integration:
	LOXY_INTEGRATION=1 HAPROXY_VERSION=$(HAPROXY_VERSION) $(GO) test -tags integration -count=1 ./test/integration/...

## dev: local stack (haproxy + whoami targets + pebble + loxy)
dev:
	docker compose -f deploy/compose/compose.dev.yml up --build

## image: production image
image:
	docker build -f deploy/docker/Dockerfile \
	  --build-arg HAPROXY_VERSION=$(HAPROXY_VERSION) \
	  --build-arg LOXY_VERSION=$(VERSION) \
	  -t $(IMAGE):$(VERSION)-haproxy$(HAPROXY_VERSION) .

## seed: demo project (§17)
seed:
	$(GO) run ./cmd/loxy admin seed $(SEED_TARGET)

## e2e: Playwright against a running Loxy (make dev or all-in-one); see test/e2e/playwright.config.ts for env vars
e2e:
	cd test/e2e && npm install --no-audit --no-fund && npx playwright test

## load: oha/k6 zero-downtime harness (placeholder until test/load exists)
load:
	@echo "load: would run 'oha -z 60s -q 2000 http://127.0.0.1:<lb-port>/' while reloading (test/load)"
	@echo "note: load harness not implemented yet (later phase); exiting 0"

clean:
	rm -rf bin coverage.out
