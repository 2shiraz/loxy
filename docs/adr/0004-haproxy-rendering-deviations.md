# ADR 0004 — Deviations from the §7.4 reference rendering

Status: accepted · 2026-10-14 (verified against haproxy:3.4.6-alpine, see docs/haproxy-notes.md)

1. **No `master-worker` keyword** in `global`: deprecated since 3.3 (removal in 3.5); `-W` on the command line is enough.
2. **`crt-base <root>/certs` + relative crt-list entries.** With absolute `live/…` paths inside crt-list files, `haproxy -c` of a staged revision resolved certificates against the *old* live tree and failed for newly added certs. Relative entries make each revision validate against its own files. Runtime commands still use the absolute `/usr/local/etc/haproxy/live/certs/<id>.pem` names (verified).
3. **Global `maxconn` is configurable** (`LOXY_HAPROXY_MAXCONN`, default 200000) because it requires a matching `nofile` ulimit.
4. **Attaching/detaching a certificate is a reload**, not runtime `add ssl crt-list` (the classifier treats any crt-list change as reload). Renewals of an attached certificate are runtime (`set ssl cert` + `commit ssl cert`). Simpler and still hitless; may be optimised later.
5. **Maintenance on TCP services** uses `tcp-request connection reject` with the same `opt@` map, since `http-request return` is HTTP-only.
6. **Drain state** is not expressible in config; it is applied at runtime and preserved across reloads by `load-server-state-from-file`.
7. **Rollback is a break-glass swap, not a desired-state rewind.** `POST /nodes/{id}/revisions/{rev}/actions/rollback` re-validates and reloads an on-disk revision, pins it and re-pushes ACME tokens, but desired state in SQLite is unchanged: the next mutation re-applies desired state. The UI/notes say so; storing the full desired state per revision (spec §8.14) is left for later.
8. **Drain** is not in the rendered files; the reconciler compares a runtime-state signature (admin states, maintenance) in addition to file hashes so drain-only changes still produce runtime ops (review fix).
