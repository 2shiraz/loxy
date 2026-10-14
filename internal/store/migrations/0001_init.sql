-- Loxy initial schema (spec §6). Timestamps are unix milliseconds.
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
  last_used_at INTEGER
);

CREATE TABLE nodes (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT 'local',
  status TEXT NOT NULL DEFAULT 'unknown',
  haproxy_version TEXT NOT NULL DEFAULT '',
  loxy_version TEXT NOT NULL DEFAULT '',
  features TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(features)),
  location_city TEXT NOT NULL DEFAULT '',
  location_country TEXT NOT NULL DEFAULT '',
  network_zone TEXT NOT NULL DEFAULT '',
  lat REAL NOT NULL DEFAULT 0,
  lon REAL NOT NULL DEFAULT 0,
  last_seen_at INTEGER,
  desired_version INTEGER NOT NULL DEFAULT 0,
  applied_version INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE networks (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  cidr TEXT NOT NULL
);

CREATE TABLE ip_addresses (
  id INTEGER PRIMARY KEY,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  address TEXT NOT NULL UNIQUE,
  family INTEGER NOT NULL CHECK (family IN (4,6)),
  kind TEXT NOT NULL DEFAULT 'additional' CHECK (kind IN ('primary','additional','failover','private')),
  network_id INTEGER REFERENCES networks(id) ON DELETE SET NULL,
  assigned_lb_id INTEGER UNIQUE REFERENCES load_balancers(id) ON DELETE SET NULL,
  rdns TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ip_addresses_node ON ip_addresses(node_id);

CREATE TABLE plans (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  max_connections INTEGER NOT NULL,
  max_services INTEGER NOT NULL,
  max_targets INTEGER NOT NULL,
  max_certificates INTEGER NOT NULL,
  traffic_quota_bytes INTEGER NOT NULL,
  price_monthly_cents INTEGER,
  currency TEXT NOT NULL DEFAULT 'EUR',
  enforce_lb_cap INTEGER NOT NULL DEFAULT 1,
  sort INTEGER NOT NULL DEFAULT 0,
  active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE load_balancers (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE RESTRICT,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT,
  algorithm TEXT NOT NULL DEFAULT 'roundrobin' CHECK (algorithm IN ('roundrobin','leastconn','source','random')),
  public_enabled INTEGER NOT NULL DEFAULT 1,
  ipv4_id INTEGER REFERENCES ip_addresses(id) ON DELETE SET NULL,
  ipv6_id INTEGER REFERENCES ip_addresses(id) ON DELETE SET NULL,
  delete_protected INTEGER NOT NULL DEFAULT 0,
  maintenance INTEGER NOT NULL DEFAULT 0,
  labels TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(labels)),
  status TEXT NOT NULL DEFAULT 'applying',
  last_error TEXT,
  version INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, name)
);
CREATE INDEX load_balancers_node ON load_balancers(node_id);

CREATE TABLE lb_networks (
  lb_id INTEGER NOT NULL REFERENCES load_balancers(id) ON DELETE CASCADE,
  network_id INTEGER NOT NULL REFERENCES networks(id) ON DELETE RESTRICT,
  ip TEXT NOT NULL,
  PRIMARY KEY (lb_id, network_id)
);

CREATE TABLE services (
  id INTEGER PRIMARY KEY,
  lb_id INTEGER NOT NULL REFERENCES load_balancers(id) ON DELETE CASCADE,
  protocol TEXT NOT NULL CHECK (protocol IN ('tcp','http','https')),
  listen_port INTEGER NOT NULL,
  destination_port INTEGER NOT NULL,
  http_redirect INTEGER NOT NULL DEFAULT 0,
  sticky_enabled INTEGER NOT NULL DEFAULT 0,
  sticky_cookie_name TEXT NOT NULL DEFAULT 'LBSTICKY',
  sticky_cookie_lifetime_s INTEGER NOT NULL DEFAULT 300,
  proxy_protocol TEXT NOT NULL DEFAULT 'none' CHECK (proxy_protocol IN ('none','v1','v2')),
  http_timeout_s INTEGER NOT NULL DEFAULT 60,
  hc_protocol TEXT NOT NULL DEFAULT 'tcp',
  hc_port INTEGER NOT NULL,
  hc_interval_s INTEGER NOT NULL DEFAULT 15,
  hc_timeout_s INTEGER NOT NULL DEFAULT 10,
  hc_retries INTEGER NOT NULL DEFAULT 3,
  hc_domain TEXT NOT NULL DEFAULT '',
  hc_path TEXT NOT NULL DEFAULT '/',
  hc_response TEXT NOT NULL DEFAULT '',
  hc_status_codes TEXT NOT NULL DEFAULT '["2??","3??"]' CHECK (json_valid(hc_status_codes)),
  hc_tls_verify INTEGER NOT NULL DEFAULT 0,
  options TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(options)),
  version INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  UNIQUE (lb_id, listen_port)
);
CREATE INDEX services_lb ON services(lb_id);

CREATE TABLE servers (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  public_ipv4 TEXT NOT NULL DEFAULT '',
  public_ipv6 TEXT NOT NULL DEFAULT '',
  private_ipv4 TEXT NOT NULL DEFAULT '',
  labels TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(labels)),
  source TEXT NOT NULL DEFAULT 'manual',
  external_id TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT ''
);

CREATE TABLE targets (
  id INTEGER PRIMARY KEY,
  lb_id INTEGER NOT NULL REFERENCES load_balancers(id) ON DELETE CASCADE,
  type TEXT NOT NULL CHECK (type IN ('server','label_selector','ip')),
  server_id INTEGER REFERENCES servers(id) ON DELETE CASCADE,
  label_selector TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '',
  use_private_ip INTEGER NOT NULL DEFAULT 0,
  weight INTEGER NOT NULL DEFAULT 100,
  backup INTEGER NOT NULL DEFAULT 0,
  admin_state TEXT NOT NULL DEFAULT 'ready' CHECK (admin_state IN ('ready','drain','maint')),
  removing INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX targets_lb ON targets(lb_id);
CREATE INDEX targets_server ON targets(server_id);

-- One row per resolved backend server; id is the stable HAProxy server id.
CREATE TABLE server_slots (
  id INTEGER PRIMARY KEY,
  lb_id INTEGER NOT NULL REFERENCES load_balancers(id) ON DELETE CASCADE,
  target_id INTEGER NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
  server_id INTEGER,
  address TEXT NOT NULL,
  UNIQUE (target_id, server_id, address)
);
CREATE INDEX server_slots_lb ON server_slots(lb_id);

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

CREATE TABLE service_certificates (
  service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  certificate_id INTEGER NOT NULL REFERENCES certificates(id) ON DELETE RESTRICT,
  position INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (service_id, certificate_id)
);
CREATE INDEX service_certificates_cert ON service_certificates(certificate_id);

CREATE TABLE domains (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  fqdn TEXT NOT NULL,
  lb_id INTEGER REFERENCES load_balancers(id) ON DELETE SET NULL,
  service_id INTEGER REFERENCES services(id) ON DELETE SET NULL,
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

CREATE TABLE metrics_1m (
  lb_id INTEGER NOT NULL, service_id INTEGER NOT NULL, ts INTEGER NOT NULL,
  open_conn_avg REAL NOT NULL, open_conn_max REAL NOT NULL, cps REAL NOT NULL, rps REAL NOT NULL,
  bytes_in REAL NOT NULL, bytes_out REAL NOT NULL, resp_time_ms_avg REAL NOT NULL,
  http_2xx REAL NOT NULL, http_3xx REAL NOT NULL, http_4xx REAL NOT NULL, http_5xx REAL NOT NULL, errors REAL NOT NULL,
  PRIMARY KEY (lb_id, service_id, ts)
) WITHOUT ROWID;
CREATE TABLE metrics_5m AS SELECT * FROM metrics_1m WHERE 0;
CREATE UNIQUE INDEX metrics_5m_pk ON metrics_5m(lb_id, service_id, ts);
CREATE TABLE metrics_1h AS SELECT * FROM metrics_1m WHERE 0;
CREATE UNIQUE INDEX metrics_1h_pk ON metrics_1h(lb_id, service_id, ts);

CREATE TABLE traffic_monthly (
  lb_id INTEGER NOT NULL,
  month TEXT NOT NULL,
  bytes_in INTEGER NOT NULL DEFAULT 0,
  bytes_out INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (lb_id, month)
);

CREATE TABLE events (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  project_id INTEGER NOT NULL DEFAULT 1,
  lb_id INTEGER,
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

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL CHECK (json_valid(value))
);
