-- 0003 query-driven indexes (verified against the SQL in internal/).

CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_key ON users (lower(email));
CREATE INDEX IF NOT EXISTS users_role_active_idx ON users (role) WHERE is_active;

CREATE INDEX IF NOT EXISTS kitchens_org_idx ON kitchens (org_id);

CREATE INDEX IF NOT EXISTS recipients_active_idx ON recipients (active) WHERE active;

CREATE INDEX IF NOT EXISTS meals_kitchen_date_idx ON meals (kitchen_id, date DESC);

CREATE INDEX IF NOT EXISTS surplus_status_idx ON surplus_batches (status);
CREATE INDEX IF NOT EXISTS surplus_kitchen_status_idx ON surplus_batches (kitchen_id, status);
CREATE INDEX IF NOT EXISTS surplus_expiry_idx ON surplus_batches (expiry_at)
    WHERE status IN ('AVAILABLE','PENDING_SAFETY','HOLD','MATCHED');
CREATE INDEX IF NOT EXISTS surplus_created_idx ON surplus_batches (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS matches_batch_idx ON matches (batch_id);
CREATE INDEX IF NOT EXISTS matches_recipient_status_idx ON matches (recipient_id, status);
CREATE INDEX IF NOT EXISTS matches_expiry_idx ON matches (expires_at) WHERE status = 'OFFERED';

CREATE INDEX IF NOT EXISTS routes_batch_idx ON routes (batch_id);
CREATE INDEX IF NOT EXISTS route_stops_route_idx ON route_stops (route_id, sequence);

CREATE INDEX IF NOT EXISTS waste_kitchen_recorded_idx ON waste (kitchen_id, recorded_at DESC);
