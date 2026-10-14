-- ===== users / sessions / tokens =====
-- name: CountUsers :one
SELECT count(*) FROM users;
-- name: CreateUser :one
INSERT INTO users (email, password_hash, role, created_at) VALUES (?, ?, ?, ?) RETURNING *;
-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;
-- name: GetUser :one
SELECT * FROM users WHERE id = ?;
-- name: ListUsers :many
SELECT * FROM users ORDER BY id;
-- name: DeleteUser :exec
DELETE FROM users WHERE id = ? AND role != 'owner';
-- name: SetUserPassword :exec
UPDATE users SET password_hash = ? WHERE id = ?;
-- name: SetUserTOTP :exec
UPDATE users SET totp_secret_enc = ? WHERE id = ?;
-- name: GetOwner :one
SELECT * FROM users WHERE role = 'owner' ORDER BY id LIMIT 1;
-- name: TouchUserLogin :exec
UPDATE users SET last_login_at = ? WHERE id = ?;
-- name: CreateSession :exec
INSERT INTO sessions (id_hash, user_id, created_at, last_seen_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?);
-- name: GetSession :one
SELECT * FROM sessions WHERE id_hash = ?;
-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE id_hash = ?;
-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = ?;
-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;
-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < ?;
-- name: ListAPITokens :many
SELECT * FROM api_tokens ORDER BY id;
-- name: CreateAPIToken :one
INSERT INTO api_tokens (project_id, name, prefix, secret_hash, role, expires_at, created_at) VALUES (1, ?, ?, ?, ?, ?, ?) RETURNING *;
-- name: GetAPITokenByPrefix :one
SELECT * FROM api_tokens WHERE prefix = ?;
-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = ? WHERE id = ?;
-- name: DeleteAPIToken :exec
DELETE FROM api_tokens WHERE id = ?;

-- ===== project / node =====
-- name: EnsureProject :exec
INSERT OR IGNORE INTO projects (id, name, created_at) VALUES (1, 'default', ?);
-- name: EnsureLocalNode :exec
INSERT OR IGNORE INTO nodes (id, name, mode) VALUES (1, 'local', 'local');
-- name: GetNode :one
SELECT * FROM nodes WHERE id = ?;
-- name: ListNodes :many
SELECT * FROM nodes ORDER BY id;
-- name: UpdateNodeInfo :exec
UPDATE nodes SET status = ?, haproxy_version = ?, loxy_version = ?, features = ?, last_seen_at = ? WHERE id = ?;
-- name: RenameNode :exec
UPDATE nodes SET name = ? WHERE id = ?;
-- name: BumpDesiredVersion :one
UPDATE nodes SET desired_version = desired_version + 1 WHERE id = ? RETURNING desired_version;
-- name: SetAppliedVersion :exec
UPDATE nodes SET applied_version = ? WHERE id = ?;
-- name: ListNodeAddresses :many
SELECT * FROM node_addresses ORDER BY id;
-- name: CreateNodeAddress :one
INSERT INTO node_addresses (node_id, address, family, label) VALUES (?, ?, ?, ?) RETURNING *;
-- name: DeleteNodeAddress :exec
DELETE FROM node_addresses WHERE id = ? AND node_id = ?;

-- ===== hosts =====
-- name: ListHosts :many
SELECT * FROM hosts ORDER BY id;
-- name: CreateHost :one
INSERT INTO hosts (project_id, name, ipv4, ipv6, private_ipv4, labels, notes) VALUES (1, ?, ?, ?, ?, ?, ?) RETURNING *;
-- name: UpdateHost :exec
UPDATE hosts SET name = ?, ipv4 = ?, ipv6 = ?, private_ipv4 = ?, labels = ?, notes = ? WHERE id = ?;
-- name: DeleteHost :exec
DELETE FROM hosts WHERE id = ?;

-- ===== backends =====
-- ===== projects (table balancers) =====
-- name: ListBalancers :many
SELECT * FROM balancers ORDER BY id;
-- name: CreateBalancer :one
INSERT INTO balancers (project_id, name, description, algorithm, labels, status, created_at, updated_at)
VALUES (1, ?, ?, ?, ?, 'applying', ?, ?) RETURNING *;
-- name: UpdateBalancer :exec
UPDATE balancers SET name = ?, description = ?, algorithm = ?, labels = ?, maintenance = ?, version = version + 1, updated_at = ? WHERE id = ?;
-- name: SetBalancerStatus :exec
UPDATE balancers SET status = ?, last_error = ? WHERE id = ?;
-- name: DeleteBalancer :exec
DELETE FROM balancers WHERE id = ?;

-- name: ListServices :many
SELECT * FROM services ORDER BY balancer_id, id;
-- name: ListListenPorts :many
SELECT listen_port FROM services ORDER BY listen_port;
-- name: CreateService :one
INSERT INTO services (balancer_id, protocol, listen_port, destination_port, proxy_protocol, timeout_s, sticky_enabled, sticky_cookie_name,
  sticky_cookie_lifetime_s, hc_protocol, hc_port, hc_interval_s, hc_timeout_s, hc_retries, hc_domain, hc_path, hc_response, hc_status_codes,
  hc_tls_verify, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;
-- name: UpdateService :exec
UPDATE services SET protocol = ?, listen_port = ?, destination_port = ?, proxy_protocol = ?, timeout_s = ?, sticky_enabled = ?, sticky_cookie_name = ?,
  sticky_cookie_lifetime_s = ?, hc_protocol = ?, hc_port = ?, hc_interval_s = ?, hc_timeout_s = ?, hc_retries = ?, hc_domain = ?, hc_path = ?,
  hc_response = ?, hc_status_codes = ?, hc_tls_verify = ?, version = version + 1 WHERE id = ?;
-- name: DeleteService :exec
DELETE FROM services WHERE id = ?;
-- name: ListServiceCertificates :many
SELECT * FROM service_certificates ORDER BY service_id, position;
-- name: ClearServiceCertificates :exec
DELETE FROM service_certificates WHERE service_id = ?;
-- name: AddServiceCertificate :exec
INSERT INTO service_certificates (service_id, certificate_id, position) VALUES (?, ?, ?);

-- name: ListTargets :many
SELECT * FROM targets ORDER BY id;
-- name: CreateTarget :one
INSERT INTO targets (balancer_id, type, host_id, label_selector, ip, port, use_private_ip, weight, backup, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;
-- name: UpdateTarget :exec
UPDATE targets SET weight = ?, backup = ?, admin_state = ? WHERE id = ?;
-- name: MarkTargetRemoving :exec
UPDATE targets SET removing = 1 WHERE id = ?;
-- name: DeleteTarget :exec
DELETE FROM targets WHERE id = ?;

-- name: ListSlots :many
SELECT * FROM target_slots ORDER BY id;
-- name: CreateSlot :one
INSERT INTO target_slots (balancer_id, target_id, host_id, address) VALUES (?, ?, ?, ?) RETURNING id;
-- name: DeleteSlot :exec
DELETE FROM target_slots WHERE id = ?;

-- ===== certificates / acme / dns / domains =====
-- name: ListCertificates :many
SELECT * FROM certificates ORDER BY id;
-- name: GetCertificate :one
SELECT * FROM certificates WHERE id = ?;
-- name: CreateCertificate :one
INSERT INTO certificates (project_id, name, type, domains, key_type, challenge, dns_provider_id, cert_pem, chain_pem, key_pem_enc, issuer, serial,
  fingerprint_sha256, not_before, not_after, status, labels, created_at)
VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;
-- name: UpdateCertificateMeta :exec
UPDATE certificates SET name = ?, labels = ? WHERE id = ?;
-- name: SetCertificateIssued :exec
UPDATE certificates SET cert_pem = ?, chain_pem = ?, key_pem_enc = ?, issuer = ?, serial = ?, fingerprint_sha256 = ?, not_before = ?, not_after = ?,
  status = 'issued', last_error = NULL, renew_after = ? WHERE id = ?;
-- name: SetCertificateStatus :exec
UPDATE certificates SET status = ?, last_error = ? WHERE id = ?;
-- name: DeleteCertificate :exec
DELETE FROM certificates WHERE id = ?;
-- name: GetACMEAccount :one
SELECT * FROM acme_accounts WHERE directory_url = ?;
-- name: CreateACMEAccount :exec
INSERT INTO acme_accounts (directory_url, email, key_pem_enc, registration) VALUES (?, ?, ?, ?);
-- name: ListDNSProviders :many
SELECT * FROM dns_providers ORDER BY id;
-- name: GetDNSProvider :one
SELECT * FROM dns_providers WHERE id = ?;
-- name: CreateDNSProvider :one
INSERT INTO dns_providers (project_id, name, kind, credentials_enc, status, last_error, last_checked_at, created_at) VALUES (1, ?, ?, ?, ?, ?, ?, ?) RETURNING *;
-- name: DeleteDNSProvider :exec
DELETE FROM dns_providers WHERE id = ?;
-- name: ListDomains :many
SELECT * FROM domains ORDER BY fqdn;
-- name: GetDomain :one
SELECT * FROM domains WHERE id = ?;
-- name: CreateDomain :one
INSERT INTO domains (project_id, fqdn, balancer_id, service_id, https, dns_provider_id, zone, auto_records, created_at, updated_at)
VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;
-- name: UpdateDomain :exec
UPDATE domains SET balancer_id = ?, service_id = ?, https = ?, dns_provider_id = ?, zone = ?, auto_records = ?, updated_at = ? WHERE id = ?;
-- name: SetDomainStatus :exec
UPDATE domains SET status = ?, status_detail = ?, observed = ?, checked_at = ? WHERE id = ?;
-- name: SetDomainCertificate :exec
UPDATE domains SET certificate_id = ? WHERE id = ?;
-- name: SetDomainManagedRecords :exec
UPDATE domains SET managed_record_ids = ? WHERE id = ?;
-- name: DeleteDomain :exec
DELETE FROM domains WHERE id = ?;

-- ===== revisions / events / audit / settings / metrics =====
-- name: CreateRevision :one
INSERT INTO config_revisions (node_id, revision, desired_version, sha256, kind, result, duration_ms, haproxy_output, state_json, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id;
-- name: ListRevisions :many
SELECT id, node_id, revision, desired_version, sha256, kind, result, duration_ms, haproxy_output, created_by, created_at, pinned
FROM config_revisions WHERE node_id = ? ORDER BY revision DESC LIMIT 100;
-- name: MaxRevision :one
SELECT CAST(coalesce(max(revision), 0) AS INTEGER) FROM config_revisions WHERE node_id = ?;
-- name: CreateEvent :one
INSERT INTO events (ts, balancer_id, kind, severity, message, data) VALUES (?, ?, ?, ?, ?, ?) RETURNING id;
-- name: ListEvents :many
SELECT * FROM events WHERE (sqlc.narg(balancer_id) IS NULL OR balancer_id = sqlc.narg(balancer_id))
  AND (sqlc.narg(kind) IS NULL OR kind = sqlc.narg(kind))
ORDER BY id DESC LIMIT sqlc.arg(max_rows);
-- name: CreateAudit :exec
INSERT INTO audit_log (ts, actor_user_id, actor_token_id, actor, ip, action, resource_type, resource_id, before, after) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
-- name: ListAudit :many
SELECT * FROM audit_log ORDER BY id DESC LIMIT ?;
-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;
-- name: PutSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value;
-- name: InsertMetric1m :exec
INSERT OR REPLACE INTO metrics_1m (scope, scope_id, ts, open_conn_avg, open_conn_max, cps, rps, bytes_in, bytes_out, resp_time_ms_avg, http_2xx, http_3xx, http_4xx, http_5xx, errors, queue)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
-- name: QueryMetrics1m :many
SELECT * FROM metrics_1m WHERE scope = ? AND scope_id = ? AND ts >= ? AND ts <= ? ORDER BY ts;
-- name: PurgeMetrics1m :exec
DELETE FROM metrics_1m WHERE ts < ?;

-- ===== notifications =====
-- name: ListChannels :many
SELECT * FROM notification_channels ORDER BY id;
-- name: GetChannel :one
SELECT * FROM notification_channels WHERE id = ?;
-- name: CreateChannel :one
INSERT INTO notification_channels (name, kind, config_enc, enabled, created_at) VALUES (?, ?, ?, ?, ?) RETURNING *;
-- name: DeleteChannel :exec
DELETE FROM notification_channels WHERE id = ?;
-- name: ListAlertRules :many
SELECT * FROM alert_rules ORDER BY id;
-- name: CreateAlertRule :one
INSERT INTO alert_rules (event_kinds, label_filter, channel_id, min_severity, cooldown_s) VALUES (?, ?, ?, ?, ?) RETURNING *;
-- name: DeleteAlertRule :exec
DELETE FROM alert_rules WHERE id = ?;
