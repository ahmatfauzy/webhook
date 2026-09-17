-- Ensure retry_policy column exists idempotently for future per-endpoint policy
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name='endpoints' AND column_name='retry_policy'
  ) THEN
    ALTER TABLE endpoints ADD COLUMN retry_policy JSONB;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name='endpoints' AND column_name='secret_version'
  ) THEN
    ALTER TABLE endpoints ADD COLUMN secret_version INT NOT NULL DEFAULT 1;
  END IF;
END $$;
