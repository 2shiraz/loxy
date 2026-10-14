-- Phase 2: notifications, alert rules, API token metadata.
CREATE TABLE notification_channels (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('webhook','email','telegram','slack')),
  config_enc BLOB NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);

CREATE TABLE alert_rules (
  id INTEGER PRIMARY KEY,
  event_kinds TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(event_kinds)),
  lb_filter TEXT NOT NULL DEFAULT '',
  channel_id INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  min_severity TEXT NOT NULL DEFAULT 'warning',
  cooldown_s INTEGER NOT NULL DEFAULT 60
);
CREATE INDEX alert_rules_channel ON alert_rules(channel_id);

ALTER TABLE api_tokens ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0;
