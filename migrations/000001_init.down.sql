DROP TRIGGER IF EXISTS trg_deliveries_updated_at ON deliveries;
DROP TRIGGER IF EXISTS trg_endpoints_updated_at ON endpoints;
DROP TRIGGER IF EXISTS trg_projects_updated_at ON projects;
DROP FUNCTION IF EXISTS set_updated_at();

DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS delivery_attempts;
DROP TABLE IF EXISTS deliveries;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS endpoints;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS projects;
