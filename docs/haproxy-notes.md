# HAProxy notes (verified behaviours)

Pinned image: `haproxy:3.4.6-alpine` (HAProxy 3.4.6-56332c5, OpenSSL 3.5.8; features include +ACME +PROMEX +QUIC +SHM_OPEN).
All items below are covered by `test/integration` (`make test-integration`, real container) unless noted.

| Spec item | Result | Test |
|---|---|---|
| §7.1 `-S /run/haproxy/master.sock,mode,660,gid,1500` | **Confirmed.** master.sock and admin.sock are created `srw-rw---- root:1500`. | `TestVerifyMasterSocketPerms` |
| §7.1 master CLI one-shot commands | **Behaviour differs from a naive client:** the master CLI only answers after the client half-closes its write side (`shutdown(SHUT_WR)`); otherwise the connection hangs. The runtime client always calls `CloseWrite()` after sending. | all integration tests |
| §7.1 synchronous `reload` | **Confirmed.** Prints `Success=1` / `Success=0`, then `--` and the startup log (image is built with SHM_OPEN). A config that passes `-c` but fails at startup (unbindable address) returns `Success=0` and the old worker keeps serving. | `TestReloadSuccessAndFailure` |
| §7.1 prompt mode on the worker socket | **Confirmed.** `prompt` → each answer ends with `\n> `; multiple commands per connection work. The collector still uses one connection per command (simpler, fast enough at 1 s). | `TestVerifyPromptMode` |
| §7.5 two `http-check expect` rules (status + string) | **Confirmed AND semantics.** 200 without the string → DOWN, 500 with the string → DOWN, 200 with the string → UP. No tcp-check fallback needed. | `TestVerifyHTTPCheckAND` |
| §7.5 `ca-file @system-ca` | **Confirmed** accepted by the image (config with `check-ssl verify required ca-file @system-ca check-sni …` loads). | `TestVerifySSLRuntimeAndMaps` |
| §7.6 dynamic servers | **Confirmed.** `add server` with the exact `render.ServerArgs` (incl. `cookie`) → `New server registered.`; `enable server`/`enable health` → empty output; `set server … weight/state` → empty; `del server` after maint → `Server deleted.` | `TestDynamicServers` |
| §7.6 SSL runtime | **Confirmed.** `set ssl cert` → "Transaction created/updated", `commit ssl cert` → `Success!`, `new ssl cert` → "New empty certificate store", `add ssl crt-list` (payload) → `Success!`, `del ssl crt-list` → "deleted in crtlist", `del ssl cert` → "deleted!" (command available). | `TestVerifySSLRuntimeAndMaps` |
| §7.6 maps | **Confirmed.** `opt@` is not part of the map name (runtime uses the plain path); `virt@acme_http01` works with `add map` and the `http-request return … lf-string` rule answers the challenge. | `TestVerifySSLRuntimeAndMaps` |
| §14.7 minimal capability set | **Confirmed.** `--user 0:0 --group-add 1500 --cap-drop ALL --cap-add NET_BIND_SERVICE,SETUID,SETGID,KILL,CHOWN` starts, binds :80 and reloads. | `TestVerifyMinimalCapabilities` |

## Other findings

- **`master-worker` in `global` is deprecated since 3.3** (warning: "will be removed in 3.5"). Loxy relies on `-W` only and does not render the keyword (deviation from the §7.4 reference text).
- **Global `maxconn 200000` requires a matching `nofile` ulimit** (≈ 2×maxconn). With a lower hard limit the worker exits ("Cannot raise FD limit"). Compose sets `nofile: 1048576`; the value is configurable (`LOXY_HAPROXY_MAXCONN`), tests use 4000.
- Starting as root without `chroot` logs a warning recommending `chroot auto`; still off by default (§7.4), to be enabled after a dedicated test.
- `server-state-file` missing on first start only logs a NOTICE.
- `docker inspect .NetworkSettings.IPAddress` is empty on Docker 29; tests read `.NetworkSettings.Networks.*.IPAddress`.

- **crt-list paths must be relative to `crt-base`.** Absolute `live/…` paths inside crt-list files made `haproxy -c` of a staged revision look at the old live tree (failed for newly attached certs). Fixed with `crt-base <root>/certs`; runtime `set ssl cert /usr/local/etc/haproxy/live/certs/<id>.pem` still matches the loaded store name (`TestVerifySSLRuntimeAndMaps`, `TestACMEHTTP01Pebble`).
- **Named defaults ambiguity (3.4 warning):** a section without `from` that implicitly uses a defaults section that is also referenced explicitly triggers a warning; every rendered section now uses `from`.
- **`show stat -1 3 -1`** (type mask frontends|backends) works and is used on alternate ticks to keep the 1 s poll cheap with 1 000+ servers; full `show stat` (servers, health) every 2 s.
- **ACME HTTP-01 via `virt@acme_http01`:** the virtual map only exists once a loaded config references it, so issuance waits until the certificate is served by an applied config (then lego → `add map virt@acme_http01` → Pebble validation → `Success`, ~9 s end-to-end in `TestACMEHTTP01Pebble`).
- **Zero-downtime:** 100 consecutive reloads plus a runtime server add/remove under ~6 000 rps (8 keep-alive clients): 884 116 requests, 0 failures (`TestApplyPathZeroDowntime`, `LOXY_IT_RELOADS=100`). Default CI run uses 20 reloads.

- **`ipv6@:::<port>` without host IPv6** makes the worker exit at startup ("protocol tcpv6: cannot create receiving socket (Address family not supported by protocol)"), although `haproxy -c` passes. IPv6 wildcard listeners are therefore rendered only when the startup probe (or `LOXY_IPV6=on`) says the host supports IPv6 (ADR 0004 §9).

## Not yet verified
- `shm-stats-file` counter preservation (§10.3) — not used.
- `chroot auto` (§7.4) — not enabled.

## Retired
- The cross-frontend connection cap (`track-sc0 … table lb<L>_cap`) was verified for the earlier load-balancer model and is no longer rendered: limits are per frontend (`maxconn`) since the frontend/backend rework (ADR 0005). Its test was removed.
