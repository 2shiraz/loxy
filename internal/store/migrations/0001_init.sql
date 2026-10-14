-- Loxy schema (spec §6). Timestamps are unix milliseconds.
CREATE TABLE projects (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('owner','admin','operator','viewer')),
  totp_secret_enc BLOB,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  last_login_at INTEGER
);

CREATE TABLE sessions (
  id_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE api_tokens (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL UNIQUE,
  secret_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  expires_at INTEGER,
  last_used_at INTEGER,
  created_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE nodes (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT 'local',
  status TEXT NOT NULL DEFAULT 'unknown',
  haproxy_version TEXT NOT NULL DEFAULT '',
  loxy_version TEXT NOT NULL DEFAULT '',
  features TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(features)),
  last_seen_at INTEGER,
  desired_version INTEGER NOT NULL DEFAULT 0,
  applied_version INTEGER NOT NULL DEFAULT 0
);

-- Addresses of the node (informational: bind suggestions, DNS required records for "*" binds).
CREATE TABLE node_addresses (
  id INTEGER PRIMARY KEY,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  address TEXT NOT NULL UNIQUE,
  family INTEGER NOT NULL CHECK (family IN (4,6)),
  label TEXT NOT NULL DEFAULT ''
);

CREATE TABLE hosts (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  ipv4 TEXT NOT NULL DEFAULT '',
  ipv6 TEXT NOT NULL DEFAULT '',
  private_ipv4 TEXT NOT NULL DEFAULT '',
  labels TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(labels)),
  notes TEXT NOT NULL DEFAULT ''
);

CREATE TABLE backends (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  node_id INTEGER NOT NULL DEFAULT 1 REFERENCES nodes(id),
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL CHECK (mode IN ('http','tcp')),
  algorithm TEXT NOT NULL DEFAULT 'roundrobin' CHECK (algorithm IN ('roundrobin','leastconn','source','random')),
  port INTEGER NOT NULL,
  sticky_enabled INTEGER NOT NULL DEFAULT 0,
  sticky_cookie_name TEXT NOT NULL DEFAULT 'LBSTICKY',
  sticky_cookie_lifetime_s INTEGER NOT NULL DEFAULT 300,
  proxy_protocol TEXT NOT NULL DEFAULT 'none' CHECK (proxy_protocol IN ('none','v1','v2')),
  timeout_server_s INTEGER NOT NULL DEFAULT 60,
  hc_protocol TEXT NOT NULL DEFAULT 'tcp',
  hc_port INTEGER NOT NULL DEFAULT 0,
  hc_interval_s INTEGER NOT NULL DEFAULT 15,
  hc_timeout_s INTEGER NOT NULL DEFAULT 10,
  hc_retries INTEGER NOT NULL DEFAULT 3,
  hc_domain TEXT NOT NULL DEFAULT '',
  hc_path TEXT NOT NULL DEFAULT '/',
  hc_response TEXT NOT NULL DEFAULT '',
  hc_status_codes TEXT NOT NULL DEFAULT '["2??","3??"]' CHECK (json_valid(hc_status_codes)),
  hc_tls_verify INTEGER NOT NULL DEFAULT 0,
  labels TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(labels)),
  version INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  UNIQUE (project_id, name)
);

CREATE TABLE frontends (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  node_id INTEGER NOT NULL DEFAULT 1 REFERENCES nodes(id),
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL CHECK (mode IN ('http','tcp')),
  tls INTEGER NOT NULL DEFAULT 0,
  binds TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(binds)),
  http_redirect INTEGER NOT NULL DEFAULT 0,
  default_backend_id INTEGER REFERENCES backends(id) ON DELETE RESTRICT,
  timeout_client_s INTEGER NOT NULL DEFAULT 60,
  maxconn INTEGER NOT NULL DEFAULT 0,
  forwarded_headers INTEGER NOT NULL DEFAULT 1,
  enabled INTEGER NOT NULL DEFAULT 1,
  maintenance INTEGER NOT NULL DEFAULT 0,
  labels TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(labels)),
  status TEXT NOT NULL DEFAULT 'applying',
  last_error TEXT,
  version INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, name)
);
CREATE INDEX frontends_default_backend ON frontends(default_backend_id);

CREATE TABLE routes (
  id INTEGER PRIMARY KEY,
  frontend_id INTEGER NOT NULL REFERENCES frontends(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  hosts TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(hosts)),
  path_prefix TEXT NOT NULL DEFAULT '',
  backend_id INTEGER NOT NULL REFERENCES backends(id) ON DELETE RESTRICT
);
CREATE INDEX routes_frontend ON routes(frontend_id);
CREATE INDEX routes_backend ON routes(backend_id);

CREATE TABLE backend_servers (
  id INTEGER PRIMARY KEY,
  backend_id INTEGER NOT NULL REFERENCES backends(id) ON DELETE CASCADE,
  type TEXT NOT NULL CHECK (type IN ('host','label_selector','ip')),
  host_id INTEGER REFERENCES hosts(id) ON DELETE CASCADE,
  label_selector TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '',
  port INTEGER,
  use_private_ip INTEGER NOT NULL DEFAULT 0,
  weight INTEGER NOT NULL DEFAULT 100,
  backup INTEGER NOT NULL DEFAULT 0,
  admin_state TEXT NOT NULL DEFAULT 'ready' CHECK (admin_state IN ('ready','drain','maint')),
  removing INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX backend_servers_backend ON backend_servers(backend_id);
CREATE INDEX backend_servers_host ON backend_servers(host_id);

-- One row per resolved server; id is the stable HAProxy server id.
CREATE TABLE server_slots (
  id INTEGER PRIMARY KEY,
  backend_id INTEGER NOT NULL REFERENCES backends(id) ON DELETE CASCADE,
  server_id INTEGER NOT NULL REFERENCES backend_servers(id) ON DELETE CASCADE,
  host_id INTEGER,
  address TEXT NOT NULL,
  UNIQUE (server_id, host_id, address)
);
CREATE INDEX server_slots_backend ON server_slots(backend_id);

CREATE TABLE dns_providers (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  credentials_enc BLOB NOT NULL,
  status TEXT NOT NULL DEFAULT 'unknown',
  last_error TEXT,
  last_checked_at INTEGER,
  created_at INTEGER NOT NULL
);

CREATE TABLE certificates (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('uploaded','managed')),
  domains TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(domains)),
  key_type TEXT NOT NULL DEFAULT 'ecdsa256',
  challenge TEXT NOT NULL DEFAULT '',
  dns_provider_id INTEGER REFERENCES dns_providers(id) ON DELETE SET NULL,
  cert_pem TEXT NOT NULL DEFAULT '',
  chain_pem TEXT NOT NULL DEFAULT '',
  key_pem_enc BLOB,
  issuer TEXT NOT NULL DEFAULT '',
  serial TEXT NOT NULL DEFAULT '',
  fingerprint_sha256 TEXT NOT NULL DEFAULT '',
  not_before INTEGER,
  not_after INTEGER,
  status TEXT NOT NULL DEFAULT 'pending',
  last_error TEXT,
  renew_after INTEGER,
  labels TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(labels)),
  created_at INTEGER NOT NULL
);

CREATE TABLE frontend_certificates (
  frontend_id INTEGER NOT NULL REFERENCES frontends(id) ON DELETE CASCADE,
  certificate_id INTEGER NOT NULL REFERENCES certificates(id) ON DELETE RESTRICT,
  position INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (frontend_id, certificate_id)
);
CREATE INDEX frontend_certificates_cert ON frontend_certificates(certificate_id);

CREATE TABLE domains (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  fqdn TEXT NOT NULL,
  frontend_id INTEGER REFERENCES frontends(id) ON DELETE SET NULL,
  dns_provider_id INTEGER REFERENCES dns_providers(id) ON DELETE SET NULL,
  zone TEXT NOT NULL DEFAULT '',
  auto_records INTEGER NOT NULL DEFAULT 0,
  ttl INTEGER NOT NULL DEFAULT 300,
  status TEXT NOT NULL DEFAULT 'unknown',
  status_detail TEXT,
  observed TEXT NOT NULL DEFAULT '{"a":[],"aaaa":[]}' CHECK (json_valid(observed)),
  checked_at INTEGER,
  certificate_id INTEGER REFERENCES certificates(id) ON DELETE SET NULL,
  managed_record_ids TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(managed_record_ids)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, fqdn)
);

CREATE TABLE acme_accounts (
  id INTEGER PRIMARY KEY,
  directory_url TEXT NOT NULL UNIQUE,
  email TEXT NOT NULL,
  key_pem_enc BLOB NOT NULL,
  registration TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE config_revisions (
  id INTEGER PRIMARY KEY,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL,
  desired_version INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  kind TEXT NOT NULL,
  result TEXT NOT NULL,
  duration_ms INTEGER NOT NULL,
  haproxy_output TEXT NOT NULL DEFAULT '',
  state_json TEXT NOT NULL DEFAULT '{}',
  created_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  pinned INTEGER NOT NULL DEFAULT 0,
  UNIQUE (node_id, revision)
);

-- scope: 'fe' (frontend) or 'be' (backend); scope_id 0 = node total.
CREATE TABLE metrics_1m (
  scope TEXT NOT NULL, scope_id INTEGER NOT NULL, ts INTEGER NOT NULL,
  open_conn_avg REAL NOT NULL, open_conn_max REAL NOT NULL, cps REAL NOT NULL, rps REAL NOT NULL,
  bytes_in REAL NOT NULL, bytes_out REAL NOT NULL, resp_time_ms_avg REAL NOT NULL,
  http_2xx REAL NOT NULL, http_3xx REAL NOT NULL, http_4xx REAL NOT NULL, http_5xx REAL NOT NULL, errors REAL NOT NULL,
  queue REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (scope, scope_id, ts)
) WITHOUT ROWID;

CREATE TABLE events (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  project_id INTEGER NOT NULL DEFAULT 1,
  frontend_id INTEGER,
  backend_id INTEGER,
  kind TEXT NOT NULL,
  severity TEXT NOT NULL,
  message TEXT NOT NULL,
  data TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(data))
);
CREATE INDEX events_ts ON events(ts);

CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  actor_user_id INTEGER,
  actor_token_id INTEGER,
  actor TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id INTEGER,
  before TEXT CHECK (before IS NULL OR json_valid(before)),
  after TEXT CHECK (after IS NULL OR json_valid(after))
);
CREATE INDEX audit_log_ts ON audit_log(ts);

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
  label_filter TEXT NOT NULL DEFAULT '',
  channel_id INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  min_severity TEXT NOT NULL DEFAULT 'warning',
  cooldown_s INTEGER NOT NULL DEFAULT 60
);
CREATE INDEX alert_rules_channel ON alert_rules(channel_id);

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL CHECK (json_valid(value))
);
