-- 0008 mobile clients (D22 refresh rotation, D23 push tokens, D24 offline ids).

CREATE TABLE refresh_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id    uuid NOT NULL,
    token_hash   text NOT NULL,          -- sha256(raw token); raw is never stored
    is_revoked   boolean NOT NULL DEFAULT false,
    expires_at   timestamptz NOT NULL,
    last_used_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT refresh_tokens_token_hash_key UNIQUE (token_hash)
);

CREATE TABLE device_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform     text NOT NULL,
    token        text NOT NULL,
    app_version  text,
    -- `is_valid` mirrors the contract's "active" flag: push is best-effort and
    -- invalid tokens are pruned when FCM reports them dead (D23).
    is_valid     boolean NOT NULL DEFAULT true,
    last_seen_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT device_tokens_token_key UNIQUE (token),
    CONSTRAINT device_tokens_platform_check CHECK (upper(platform) IN ('ANDROID','IOS'))
);

-- Offline-created rows can be replayed; the unique id makes replay idempotent.
ALTER TABLE waste            ADD COLUMN IF NOT EXISTS client_event_id text UNIQUE;
ALTER TABLE surplus_batches  ADD COLUMN IF NOT EXISTS client_event_id text UNIQUE;
ALTER TABLE qr_events        ADD COLUMN IF NOT EXISTS client_event_id text UNIQUE;
ALTER TABLE qr_events        ADD COLUMN IF NOT EXISTS client_ts timestamptz;
ALTER TABLE matches          ADD COLUMN IF NOT EXISTS client_ts timestamptz;

CREATE INDEX IF NOT EXISTS refresh_tokens_user_idx   ON refresh_tokens (user_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_family_idx ON refresh_tokens (family_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_expires_idx ON refresh_tokens (expires_at);
CREATE INDEX IF NOT EXISTS device_tokens_user_valid_idx ON device_tokens (user_id) WHERE is_valid;

CREATE TRIGGER device_tokens_updated_at
    BEFORE UPDATE ON device_tokens FOR EACH ROW EXECUTE FUNCTION set_updated_at();
