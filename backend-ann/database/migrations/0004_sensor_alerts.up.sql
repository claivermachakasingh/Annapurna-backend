-- 0004 IoT telemetry and alerting.

CREATE TABLE sensor_readings (
    id            bigserial PRIMARY KEY,
    sensor_id     text,
    device_id     text,
    location_id   text,
    kitchen_id    uuid REFERENCES kitchens(id) ON DELETE SET NULL,
    batch_id      uuid REFERENCES surplus_batches(id) ON DELETE SET NULL,
    sensor_type   text,
    ts            timestamptz NOT NULL DEFAULT now(),
    recorded_at   timestamptz NOT NULL DEFAULT now(),
    temperature_c numeric(5,2),
    humidity_pct  numeric(5,2),
    energy_kwh    numeric(10,3),
    value         numeric(10,3),
    unit          text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sensor_readings_temp_check CHECK (temperature_c IS NULL OR (temperature_c >= -30 AND temperature_c <= 120)),
    CONSTRAINT sensor_readings_humidity_check CHECK (humidity_pct IS NULL OR (humidity_pct >= 0 AND humidity_pct <= 100)),
    CONSTRAINT sensor_readings_energy_check CHECK (energy_kwh IS NULL OR energy_kwh >= 0)
);

CREATE TABLE alerts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kitchen_id      uuid REFERENCES kitchens(id) ON DELETE CASCADE,
    type            text,
    alert_type      text,
    severity        text NOT NULL DEFAULT 'WARN',
    message         text NOT NULL,
    payload         jsonb,
    is_acked        boolean NOT NULL DEFAULT false,
    acknowledged_by uuid REFERENCES users(id) ON DELETE SET NULL,
    acked_by        uuid REFERENCES users(id) ON DELETE SET NULL,
    acknowledged_at timestamptz,
    acked_at        timestamptz,
    dedupe_key      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT alerts_severity_check CHECK (severity IN ('INFO','WARN','CRITICAL')),
    CONSTRAINT alerts_type_check CHECK (
        (COALESCE(type, alert_type)) IS NULL OR COALESCE(type, alert_type) IN (
        'TEMP_EXCURSION','HUMIDITY_HIGH','ENERGY_SPIKE','DOWNTIME_HIGH',
        'MATERIAL_LOSS_HIGH','EXPIRY_SOON','SENSOR_STALE'))
);

CREATE TRIGGER alerts_updated_at BEFORE UPDATE ON alerts FOR EACH ROW EXECUTE FUNCTION set_updated_at();
