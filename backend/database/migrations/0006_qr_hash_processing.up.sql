-- 0006 tamper-evident QR custody chain (D30) and processing KPIs (D3/D9).
--
-- Hash input (canonical, ASCII concatenation):
--   sha256(prev_hash || batch_id || event_type || actor_id || server_ts_ISO || evidence_hash)
-- client_ts / client_event_id are provenance only and are NEVER hashed (D24).

CREATE TABLE qr_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id        uuid NOT NULL REFERENCES surplus_batches(id) ON DELETE CASCADE,
    event_type      text NOT NULL,
    actor_id        uuid,
    actor_role      text,
    lat             double precision,
    lng             double precision,
    evidence_hash   text,
    prev_hash       text NOT NULL,
    event_hash      text NOT NULL,
    hash            text,
    server_ts       timestamptz NOT NULL DEFAULT now(),
    client_event_id text UNIQUE,
    client_ts       timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT qr_events_type_check CHECK (event_type IN (
        'CREATED','APPROVED','MATCHED','PICKED_UP','HANDED_OFF','RECEIVED'))
);

CREATE INDEX IF NOT EXISTS qr_events_batch_created_idx ON qr_events (batch_id, created_at, id);

-- Append-only: the chain must never be rewritten. TRUNCATE is still allowed so
-- that dev databases can be reset; UPDATE/DELETE of individual rows is not.
CREATE OR REPLACE FUNCTION qr_events_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'qr_events is append-only (attempted %)', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER qr_events_immutable_trg
    BEFORE UPDATE OR DELETE ON qr_events
    FOR EACH ROW EXECUTE FUNCTION qr_events_immutable();

CREATE TABLE processing_metrics (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kitchen_id        uuid NOT NULL REFERENCES kitchens(id) ON DELETE CASCADE,
    date              date,
    period_start      date,
    period_end        date,
    raw_material_kg   numeric(10,2) NOT NULL DEFAULT 0,
    output_kg         numeric(10,2) NOT NULL DEFAULT 0,
    waste_kg          numeric(10,2) NOT NULL DEFAULT 0,
    downtime_min      numeric(10,2) NOT NULL DEFAULT 0,
    runtime_min       numeric(10,2) NOT NULL DEFAULT 0,
    energy_kwh        numeric(10,3) NOT NULL DEFAULT 0,
    material_loss_pct numeric(6,2),
    downtime_pct      numeric(6,2),
    energy_per_kg     numeric(10,4),
    efficiency_score  numeric(6,2),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT processing_raw_check      CHECK (raw_material_kg >= 0 AND output_kg >= 0 AND waste_kg >= 0),
    CONSTRAINT processing_downtime_check CHECK (downtime_min >= 0 AND runtime_min >= 0),
    CONSTRAINT processing_output_check   CHECK (output_kg <= raw_material_kg OR raw_material_kg = 0),
    CONSTRAINT processing_period_key     UNIQUE (kitchen_id, period_start, period_end)
);

CREATE UNIQUE INDEX IF NOT EXISTS processing_metrics_kitchen_date_key
    ON processing_metrics (kitchen_id, date) WHERE date IS NOT NULL;

CREATE TRIGGER processing_metrics_updated_at
    BEFORE UPDATE ON processing_metrics FOR EACH ROW EXECUTE FUNCTION set_updated_at();
