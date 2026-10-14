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
INSERT INTO alert_rules (event_kinds, lb_filter, channel_id, min_severity, cooldown_s) VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: DeleteAlertRule :exec
DELETE FROM alert_rules WHERE id = ?;

-- name: SetUserTOTP :exec
UPDATE users SET totp_secret_enc = ? WHERE id = ?;
