# ADR 0001 — Implementation choices within the decided stack (§4)

Status: accepted · 2026-10-14

## Decisions
1. **API layer.** `oapi-codegen` generates the std-http `ServerInterface`, router, path/query binding and request models from `api/openapi.yaml` (`internal/api/gen`). Handlers build response bodies as small maps/structs that follow the same schemas (the generated response types are mostly pointers and would add noise). `make gen-check` keeps the generated code fresh.
2. **sqlc + SQLite.** Queries in `internal/store/queries`, output committed in `internal/store/db`. sqlc's SQLite engine mis-numbers mixed `sqlc.narg()` and `?` parameters, so filtered queries use only named args.
3. **Integration tests drive Docker directly** (`docker run` via `os/exec`) instead of `testcontainers-go`: same coverage against the pinned image, no extra test dependency tree. Tests are behind the `integration` build tag and `LOXY_INTEGRATION=1`.
4. **npm instead of pnpm** for the web build (lockfile `web/package-lock.json`); one less tool in the image build.
5. **Metrics retention.** Only `metrics_1m` is written (kept 35 days); 5 m / 1 h resolutions are aggregated on read. `metrics_5m`/`metrics_1h` tables exist for a later rollup job if long ranges get slow.
6. **Optimistic concurrency.** `If-Match` is honoured when sent (409 on mismatch) but not required, so simple scripts keep working.
7. **Secret key fallback.** If `LOXY_SECRET_KEY` is missing, loxy generates one into `$LOXY_DATA_DIR/secret.key` (0600) and logs a warning, instead of refusing to start.
8. **Panel cert / placeholder cert** are generated once, encrypted in `settings`, so renders are deterministic.
