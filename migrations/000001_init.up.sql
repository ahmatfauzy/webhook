-- Webhooker initial schema
-- FK CASCADE except events.project_id RESTRICT, plain ULID TEXT PKs, retention via batch CTE

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- projects
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_projects_created_at ON projects (created_at);

-- api_keys
CREATE TABLE api_keys (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    key_prefix TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);
CREATE INDEX idx_api_keys_project_id ON api_keys (project_id);
CREATE INDEX idx_api_keys_key_hash ON api_keys (key_hash);
CREATE INDEX idx_api_keys_key_prefix ON api_keys (key_prefix);

-- endpoints
CREATE TABLE endpoints (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT,
    url TEXT NOT NULL,
    secret_encrypted TEXT NOT NULL,
    secret_version INT NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','disabled')),
    timeout_ms INT NOT NULL DEFAULT 10000,
    retry_policy JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_endpoints_project_id ON endpoints (project_id);
CREATE INDEX idx_endpoints_project_created ON endpoints (project_id, created_at DESC);
-- duplicate URL allowed per project, so no unique on (project_id,url)

-- subscriptions
CREATE TABLE subscriptions (
    id TEXT PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (endpoint_id, event_type)
);
CREATE INDEX idx_subscriptions_endpoint_id ON subscriptions (endpoint_id);
CREATE INDEX idx_subscriptions_event_type ON subscriptions (event_type);

-- events (immutable)
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    type TEXT NOT NULL CHECK (type ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$' AND length(type) <= 128),
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_events_project_type_created ON events (project_id, type, created_at DESC);
CREATE INDEX idx_events_project_created ON events (project_id, created_at DESC);
CREATE INDEX idx_events_created_at ON events (created_at DESC);

-- deliveries
CREATE TABLE deliveries (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    endpoint_id TEXT NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','delivered','retrying','dead_letter')),
    attempt_count INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_deliveries_event_id ON deliveries (event_id);
CREATE INDEX idx_deliveries_endpoint_created ON deliveries (endpoint_id, created_at DESC);
CREATE INDEX idx_deliveries_created_at ON deliveries (created_at DESC);
-- partial index for retry poller
CREATE INDEX idx_deliveries_retrying_next_attempt ON deliveries (status, next_attempt_at) WHERE status = 'retrying';
-- for endpoint isolation via join, also index status alone for dashboard filters
CREATE INDEX idx_deliveries_status ON deliveries (status);

-- delivery_attempts
CREATE TABLE delivery_attempts (
    id TEXT PRIMARY KEY,
    delivery_id TEXT NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
    attempt_number INT NOT NULL,
    status TEXT,
    http_status INT,
    request_headers JSONB,
    response_headers JSONB,
    response_body TEXT,
    error TEXT,
    duration_ms INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (delivery_id, attempt_number)
);
CREATE INDEX idx_delivery_attempts_delivery_id ON delivery_attempts (delivery_id);

-- idempotency_keys
CREATE TABLE idempotency_keys (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE (project_id, key)
);
CREATE INDEX idx_idempotency_keys_project_id ON idempotency_keys (project_id);
CREATE INDEX idx_idempotency_keys_expires_at ON idempotency_keys (expires_at);
CREATE INDEX idx_idempotency_keys_event_id ON idempotency_keys (event_id);

-- updated_at trigger helper
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_projects_updated_at BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_endpoints_updated_at BEFORE UPDATE ON endpoints FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_deliveries_updated_at BEFORE UPDATE ON deliveries FOR EACH ROW EXECUTE FUNCTION set_updated_at();
