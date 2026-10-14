-- Projects (ADR 0006): a project is one load balancer with targets, services and domains.
-- The table is `balancers` because `projects` is the tenancy table. Frontend/backend
-- definitions from the previous model are dropped; domains are kept but detached.

CREATE TABLE balancers (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL DEFAULT 1 REFERENCES projects(id) ON DELETE CASCADE,
  node_id INTEGER NOT NULL DEFAULT 1 REFERENCES nodes(id),
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  algorithm TEXT NOT NULL DEFAULT 'roundrobin' CHECK (algorithm IN ('roundrobin','leastconn','source','random')),
  labels TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(labels)),
  maintenance INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'applying',
  last_error TEXT,
  version INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (project_id, name)
);

CREATE TABLE services (
  id INTEGER PRIMARY KEY,
  balancer_id INTEGER NOT NULL REFERENCES balancers(id) ON DELETE CASCADE,
  protocol TEXT NOT NULL CHECK (protocol IN ('http','https','tcp')),
  listen_port INTEGER NOT NULL UNIQUE,
  destination_port INTEGER NOT NULL,
  proxy_protocol TEXT NOT NULL DEFAULT 'none' CHECK (proxy_protocol IN ('none','v1','v2')),
  timeout_s INTEGER NOT NULL DEFAULT 60,
  sticky_enabled INTEGER NOT NULL DEFAULT 0,
  sticky_cookie_name TEXT NOT NULL DEFAULT 'LOXYSTICKY',
  sticky_cookie_lifetime_s INTEGER NOT NULL DEFAULT 300,
  hc_protocol TEXT NOT NULL DEFAULT 'tcp' CHECK (hc_protocol IN ('tcp','http','https')),
  hc_port INTEGER NOT NULL DEFAULT 0,
  hc_interval_s INTEGER NOT NULL DEFAULT 15,
  hc_timeout_s INTEGER NOT NULL DEFAULT 10,
  hc_retries INTEGER NOT NULL DEFAULT 3,
  hc_domain TEXT NOT NULL DEFAULT '',
  hc_path TEXT NOT NULL DEFAULT '/',
  hc_response TEXT NOT NULL DEFAULT '',
  hc_status_codes TEXT NOT NULL DEFAULT '["2??","3??"]' CHECK (json_valid(hc_status_codes)),
  hc_tls_verify INTEGER NOT NULL DEFAULT 0,
  version INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);
CREATE INDEX services_balancer ON services(balancer_id);

CREATE TABLE service_certificates (
  service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  certificate_id INTEGER NOT NULL REFERENCES certificates(id) ON DELETE RESTRICT,
  position INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (service_id, certificate_id)
);
CREATE INDEX service_certificates_cert ON service_certificates(certificate_id);

CREATE TABLE targets (
  id INTEGER PRIMARY KEY,
  balancer_id INTEGER NOT NULL REFERENCES balancers(id) ON DELETE CASCADE,
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
CREATE INDEX targets_balancer ON targets(balancer_id);
CREATE INDEX targets_host ON targets(host_id);

-- One row per resolved target address; id is the stable HAProxy server id.
CREATE TABLE target_slots (
  id INTEGER PRIMARY KEY,
  balancer_id INTEGER NOT NULL REFERENCES balancers(id) ON DELETE CASCADE,
  target_id INTEGER NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
  host_id INTEGER,
  address TEXT NOT NULL,
  UNIQUE (target_id, host_id, address)
);
CREATE INDEX target_slots_balancer ON target_slots(balancer_id);

-- Domains: re-created without the frontend reference.
CREATE TABLE domains_new (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  fqdn TEXT NOT NULL,
  balancer_id INTEGER REFERENCES balancers(id) ON DELETE SET NULL,
  service_id INTEGER REFERENCES services(id) ON DELETE SET NULL,
  https INTEGER NOT NULL DEFAULT 1,
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
INSERT INTO domains_new (id, project_id, fqdn, dns_provider_id, zone, auto_records, ttl, status, status_detail, observed, checked_at,
  certificate_id, managed_record_ids, created_at, updated_at)
  SELECT id, project_id, fqdn, dns_provider_id, zone, auto_records, ttl, status, status_detail, observed, checked_at,
  certificate_id, managed_record_ids, created_at, updated_at FROM domains;
DROP TABLE domains;
ALTER TABLE domains_new RENAME TO domains;
CREATE INDEX domains_balancer ON domains(balancer_id);

-- Events: per-project instead of per-frontend/backend.
CREATE TABLE events_new (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  project_id INTEGER NOT NULL DEFAULT 1,
  balancer_id INTEGER,
  kind TEXT NOT NULL,
  severity TEXT NOT NULL,
  message TEXT NOT NULL,
  data TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(data))
);
INSERT INTO events_new (id, ts, project_id, kind, severity, message, data) SELECT id, ts, project_id, kind, severity, message, data FROM events;
DROP TABLE events;
ALTER TABLE events_new RENAME TO events;
CREATE INDEX events_ts ON events(ts);
CREATE INDEX events_balancer ON events(balancer_id, ts);

DROP TABLE routes;
DROP TABLE frontend_certificates;
DROP TABLE frontends;
DROP TABLE server_slots;
DROP TABLE backend_servers;
DROP TABLE backends;
DELETE FROM metrics_1m;
-- Health events are now per target of a project.
UPDATE alert_rules SET event_kinds = replace(event_kinds, 'server.health.changed', 'target.health.changed');
