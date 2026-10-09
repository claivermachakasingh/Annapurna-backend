-- 0007 append-only audit trail and recovery-hierarchy diversions.

CREATE TABLE audit_log (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid,
    actor_id    uuid,
    action      text NOT NULL,
    entity      text,
    resource_type text,
    entity_id   text,
    resource_id text,
    before      jsonb,
    after       jsonb,
    metadata    jsonb,
    ip          text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE diversions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id     uuid NOT NULL REFERENCES surplus_batches(id) ON DELETE CASCADE,
    stream       text,
    from_status  text,
    to_status    text,
    recipient_id uuid REFERENCES recipients(id) ON DELETE SET NULL,
    quantity_kg  numeric(10,2),
    reason       text,
    actor_id     uuid,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT diversions_stream_check CHECK (
        stream IS NULL OR stream IN ('ANIMAL_FEED','COMPOST','BIOGAS'))
);

CREATE INDEX IF NOT EXISTS audit_log_entity_idx ON audit_log (entity, entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS audit_log_user_idx ON audit_log (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS diversions_batch_idx ON diversions (batch_id, created_at DESC);
