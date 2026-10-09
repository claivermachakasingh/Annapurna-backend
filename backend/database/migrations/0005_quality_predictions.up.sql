-- 0005 quality evidence, demand prediction log, outcome feedback.

CREATE TABLE quality_checks (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id            uuid NOT NULL REFERENCES surplus_batches(id) ON DELETE CASCADE,
    image_path          text,
    visual_status       text NOT NULL,
    risk_level          text NOT NULL,
    cv_confidence       numeric(5,4),
    detections          jsonb,
    cv_model_version    text,
    safety_decision     text NOT NULL,
    safety_reasons      text[],
    fusion_model_version text,
    danger_zone_minutes numeric(10,2) NOT NULL DEFAULT 0,
    temperature_c       numeric(5,2),
    device_preview      jsonb,   -- device-side preview: telemetry ONLY (D20)
    client_meta         jsonb,
    created_by          uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT quality_visual_check   CHECK (visual_status IN ('GOOD','RISK','REJECTED')),
    CONSTRAINT quality_risk_check     CHECK (risk_level IN ('LOW','MEDIUM','HIGH')),
    CONSTRAINT quality_decision_check CHECK (safety_decision IN ('ELIGIBLE','HOLD','REJECTED'))
);

CREATE TABLE prediction_log (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kitchen_id    uuid REFERENCES kitchens(id) ON DELETE CASCADE,
    meal_id       uuid REFERENCES meals(id) ON DELETE SET NULL,
    request       jsonb,
    response      jsonb,
    model_version text,
    predicted_kg  numeric(10,2),
    predicted_for date,
    actual_kg     numeric(10,2),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE outcome_feedback (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meal_id        uuid REFERENCES meals(id) ON DELETE SET NULL,
    match_id       uuid REFERENCES matches(id) ON DELETE SET NULL,
    recipient_id   uuid REFERENCES recipients(id) ON DELETE SET NULL,
    predicted_p50  numeric(10,2),
    actual_consumed numeric(10,2),
    prepared       numeric(10,2),
    rating         integer,
    comment        text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outcome_feedback_rating_check CHECK (rating IS NULL OR (rating >= 1 AND rating <= 5)),
    CONSTRAINT outcome_feedback_match_recipient_key UNIQUE (match_id, recipient_id)
);

CREATE INDEX IF NOT EXISTS quality_checks_batch_created_idx ON quality_checks (batch_id, created_at DESC);
CREATE INDEX IF NOT EXISTS prediction_log_kitchen_created_idx ON prediction_log (kitchen_id, created_at DESC);
