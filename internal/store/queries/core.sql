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

-- name: UpdateNodeLocation :exec
UPDATE nodes SET name = ?, location_city = ?, location_country = ?, network_zone = ?, lat = ?, lon = ? WHERE id = ?;

-- name: BumpDesiredVersion :one
UPDATE nodes SET desired_version = desired_version + 1 WHERE id = ? RETURNING desired_version;

-- name: SetAppliedVersion :exec
UPDATE nodes SET applied_version = ? WHERE id = ?;

-- name: ListIPs :many
SELECT * FROM ip_addresses ORDER BY id;

-- name: CreateIP :one
INSERT INTO ip_addresses (node_id, address, family, kind) VALUES (?, ?, ?, ?) RETURNING *;

-- name: DeleteIP :exec
DELETE FROM ip_addresses WHERE id = ? AND node_id = ? AND assigned_lb_id IS NULL;

-- name: AssignIP :exec
UPDATE ip_addresses SET assigned_lb_id = ? WHERE id = ?;

-- name: ReleaseLBIPs :exec
UPDATE ip_addresses SET assigned_lb_id = NULL WHERE assigned_lb_id = ?;

-- name: ListPlans :many
SELECT * FROM plans WHERE active = 1 ORDER BY sort, id;

-- name: CountPlans :one
SELECT count(*) FROM plans;

-- name: CreatePlan :exec
INSERT INTO plans (name, max_connections, max_services, max_targets, max_certificates, traffic_quota_bytes, price_monthly_cents, sort) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListLoadBalancers :many
SELECT * FROM load_balancers ORDER BY id;

-- name: CreateLoadBalancer :one
INSERT INTO load_balancers (project_id, name, plan_id, node_id, algorithm, public_enabled, ipv4_id, ipv6_id, labels, status, created_at, updated_at)
VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, 'applying', ?, ?) RETURNING *;

-- name: UpdateLoadBalancer :exec
UPDATE load_balancers SET name = ?, plan_id = ?, algorithm = ?, public_enabled = ?, delete_protected = ?, maintenance = ?, labels = ?, version = version + 1, updated_at = ? WHERE id = ?;

-- name: SetLoadBalancerStatus :exec
UPDATE load_balancers SET status = ?, last_error = ? WHERE id = ?;

-- name: SetLoadBalancersStatusByNode :exec
UPDATE load_balancers SET status = ?, last_error = ? WHERE node_id = ?;

-- name: DeleteLoadBalancer :exec
DELETE FROM load_balancers WHERE id = ?;

-- name: ListLBNetworks :many
SELECT * FROM lb_networks ORDER BY lb_id, network_id;

-- name: AttachLBNetwork :exec
INSERT INTO lb_networks (lb_id, network_id, ip) VALUES (?, ?, ?);

-- name: DetachLBNetwork :exec
DELETE FROM lb_networks WHERE lb_id = ? AND network_id = ?;

-- name: ListServices :many
SELECT * FROM services ORDER BY id;

-- name: CreateService :one
INSERT INTO services (lb_id, protocol, listen_port, destination_port, http_redirect, sticky_enabled, sticky_cookie_name, sticky_cookie_lifetime_s,
  proxy_protocol, http_timeout_s, hc_protocol, hc_port, hc_interval_s, hc_timeout_s, hc_retries, hc_domain, hc_path, hc_response, hc_status_codes, hc_tls_verify, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateService :exec
UPDATE services SET protocol = ?, listen_port = ?, destination_port = ?, http_redirect = ?, sticky_enabled = ?, sticky_cookie_name = ?, sticky_cookie_lifetime_s = ?,
  proxy_protocol = ?, http_timeout_s = ?, hc_protocol = ?, hc_port = ?, hc_interval_s = ?, hc_timeout_s = ?, hc_retries = ?, hc_domain = ?, hc_path = ?, hc_response = ?,
  hc_status_codes = ?, hc_tls_verify = ?, version = version + 1 WHERE id = ?;

-- name: DeleteService :exec
DELETE FROM services WHERE id = ?;

-- name: ListServiceCertificates :many
SELECT * FROM service_certificates ORDER BY service_id, position;

-- name: ClearServiceCertificates :exec
DELETE FROM service_certificates WHERE service_id = ?;

-- name: AddServiceCertificate :exec
INSERT INTO service_certificates (service_id, certificate_id, position) VALUES (?, ?, ?);

-- name: ListServers :many
SELECT * FROM servers ORDER BY id;

-- name: CreateServer :one
INSERT INTO servers (project_id, name, public_ipv4, public_ipv6, private_ipv4, labels, notes) VALUES (1, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateServer :exec
UPDATE servers SET name = ?, public_ipv4 = ?, public_ipv6 = ?, private_ipv4 = ?, labels = ?, notes = ? WHERE id = ?;

-- name: DeleteServer :exec
DELETE FROM servers WHERE id = ?;

-- name: ListTargets :many
SELECT * FROM targets ORDER BY id;

-- name: CreateTarget :one
INSERT INTO targets (lb_id, type, server_id, label_selector, ip, use_private_ip, weight, backup, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateTarget :exec
UPDATE targets SET weight = ?, backup = ?, admin_state = ? WHERE id = ?;

-- name: MarkTargetRemoving :exec
UPDATE targets SET removing = 1 WHERE id = ?;

-- name: DeleteTarget :exec
DELETE FROM targets WHERE id = ?;

-- name: ListSlots :many
SELECT * FROM server_slots ORDER BY id;

-- name: CreateSlot :one
INSERT INTO server_slots (lb_id, target_id, server_id, address) VALUES (?, ?, ?, ?) RETURNING id;

-- name: DeleteSlot :exec
DELETE FROM server_slots WHERE id = ?;

-- name: ListNetworks :many
SELECT * FROM networks ORDER BY id;

-- name: CreateNetwork :one
INSERT INTO networks (project_id, name, cidr) VALUES (1, ?, ?) RETURNING *;

-- name: DeleteNetwork :exec
DELETE FROM networks WHERE id = ?;

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
INSERT INTO domains (project_id, fqdn, lb_id, service_id, dns_provider_id, zone, auto_records, created_at, updated_at) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateDomain :exec
UPDATE domains SET lb_id = ?, service_id = ?, dns_provider_id = ?, zone = ?, auto_records = ?, updated_at = ? WHERE id = ?;

-- name: SetDomainStatus :exec
UPDATE domains SET status = ?, status_detail = ?, observed = ?, checked_at = ? WHERE id = ?;

-- name: SetDomainCertificate :exec
UPDATE domains SET certificate_id = ? WHERE id = ?;

-- name: SetDomainManagedRecords :exec
UPDATE domains SET managed_record_ids = ? WHERE id = ?;

-- name: DeleteDomain :exec
DELETE FROM domains WHERE id = ?;

-- name: CreateRevision :one
INSERT INTO config_revisions (node_id, revision, desired_version, sha256, kind, result, duration_ms, haproxy_output, state_json, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id;

-- name: ListRevisions :many
SELECT id, node_id, revision, desired_version, sha256, kind, result, duration_ms, haproxy_output, created_by, created_at, pinned
FROM config_revisions WHERE node_id = ? ORDER BY revision DESC LIMIT 100;

-- name: GetRevision :one
SELECT * FROM config_revisions WHERE node_id = ? AND revision = ?;

-- name: MaxRevision :one
SELECT CAST(coalesce(max(revision), 0) AS INTEGER) FROM config_revisions WHERE node_id = ?;

-- name: CreateEvent :one
INSERT INTO events (ts, lb_id, kind, severity, message, data) VALUES (?, ?, ?, ?, ?, ?) RETURNING id;

-- name: ListEvents :many
SELECT * FROM events WHERE (sqlc.narg(lb_id) IS NULL OR lb_id = sqlc.narg(lb_id)) AND (sqlc.narg(kind) IS NULL OR kind = sqlc.narg(kind))
ORDER BY id DESC LIMIT ?;

-- name: CreateAudit :exec
INSERT INTO audit_log (ts, actor_user_id, actor_token_id, actor, ip, action, resource_type, resource_id, before, after) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAudit :many
SELECT * FROM audit_log ORDER BY id DESC LIMIT ?;

-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;

-- name: PutSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value;

-- name: InsertMetric1m :exec
INSERT OR REPLACE INTO metrics_1m (lb_id, service_id, ts, open_conn_avg, open_conn_max, cps, rps, bytes_in, bytes_out, resp_time_ms_avg, http_2xx, http_3xx, http_4xx, http_5xx, errors)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: QueryMetrics1m :many
SELECT * FROM metrics_1m WHERE lb_id = ? AND service_id = ? AND ts >= ? AND ts <= ? ORDER BY ts;

-- name: PurgeMetrics1m :exec
DELETE FROM metrics_1m WHERE ts < ?;

-- name: AddTraffic :exec
INSERT INTO traffic_monthly (lb_id, month, bytes_in, bytes_out) VALUES (?, ?, ?, ?)
ON CONFLICT(lb_id, month) DO UPDATE SET bytes_in = bytes_in + excluded.bytes_in, bytes_out = bytes_out + excluded.bytes_out;

-- name: ListTrafficMonth :many
SELECT * FROM traffic_monthly WHERE month = ?;
