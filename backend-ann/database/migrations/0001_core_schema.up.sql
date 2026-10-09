-- 0001 core schema (D14): organisations, kitchens, users, recipients, meals,
-- attendance, production, waste, surplus_batches, matches, routes, route_stops.
-- UUID PKs via pgcrypto; all timestamps timestamptz (UTC).

-- No extension needed: gen_random_uuid() is core since PostgreSQL 13.
-- (The v2 spec's `CREATE EXTENSION pgcrypto` is a no-op on PG16 and fails on
-- builds that ship without contrib, e.g. the portable binaries used in CI.)

-- updated_at maintenance
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE organisations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    type       text NOT NULL DEFAULT 'OTHER',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE kitchens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid REFERENCES organisations(id) ON DELETE SET NULL,
    name       text NOT NULL,
    type       text NOT NULL DEFAULT 'HOSTEL',
    latitude   double precision,
    longitude  double precision,
    timezone   text NOT NULL DEFAULT 'Asia/Kolkata',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- users.org_id carries the scope id: a kitchen id for KITCHEN users and a
-- recipient id for NGO users (this is what the access token is scoped by).
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid,
    kitchen_id    uuid REFERENCES kitchens(id) ON DELETE SET NULL,
    recipient_id  uuid,
    name          text,
    email         text NOT NULL,
    password_hash text NOT NULL,
    role          text NOT NULL,
    is_active     boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE recipients (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              uuid REFERENCES organisations(id) ON DELETE SET NULL,
    name                text NOT NULL,
    type                text NOT NULL DEFAULT 'NGO',
    capacity_kg         numeric(10,2) NOT NULL DEFAULT 0,
    latitude            double precision NOT NULL,
    longitude           double precision NOT NULL,
    pickup_window_start time NOT NULL DEFAULT '09:00',
    pickup_window_end   time NOT NULL DEFAULT '18:00',
    accepts_categories  text[] NOT NULL DEFAULT ARRAY['cooked','packaged','raw'],
    last_received_at    timestamptz,
    active              boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE users
    ADD CONSTRAINT users_recipient_id_fkey
    FOREIGN KEY (recipient_id) REFERENCES recipients(id) ON DELETE SET NULL;

CREATE TABLE meals (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kitchen_id  uuid NOT NULL REFERENCES kitchens(id) ON DELETE RESTRICT,
    date        date NOT NULL,
    meal_type   text NOT NULL,
    menu        jsonb NOT NULL DEFAULT '[]'::jsonb,
    name        text,
    description text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kitchen_id, date, meal_type)
);

CREATE TABLE attendance (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meal_id        uuid REFERENCES meals(id) ON DELETE CASCADE,
    kitchen_id     uuid REFERENCES kitchens(id) ON DELETE CASCADE,
    meal_date      date,
    expected_diners integer,
    actual_diners  integer,
    head_count     integer,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kitchen_id, meal_date)
);

CREATE TABLE production (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meal_id         uuid REFERENCES meals(id) ON DELETE SET NULL,
    kitchen_id      uuid REFERENCES kitchens(id) ON DELETE CASCADE,
    production_date date,
    prepared_qty    numeric(10,2),
    consumed_qty    numeric(10,2),
    quantity_kg     numeric(10,2),
    unit            text NOT NULL DEFAULT 'kg',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kitchen_id, meal_id, production_date)
);

CREATE TABLE waste (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meal_id         uuid REFERENCES meals(id) ON DELETE SET NULL,
    kitchen_id      uuid NOT NULL REFERENCES kitchens(id) ON DELETE CASCADE,
    food_type       text,
    quantity_kg     numeric(10,2) NOT NULL,
    cause           text,
    waste_type      text,
    note            text,
    recorded_at     timestamptz,
    client_event_id text UNIQUE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE surplus_batches (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_code      text NOT NULL,
    meal_id         uuid REFERENCES meals(id) ON DELETE SET NULL,
    kitchen_id      uuid REFERENCES kitchens(id) ON DELETE SET NULL,
    food_name       text NOT NULL,
    food_category   text NOT NULL DEFAULT 'cooked',
    quantity_kg     numeric(10,2) NOT NULL,
    prepared_at     timestamptz NOT NULL,
    expiry_at       timestamptz NOT NULL,
    safety_status   text NOT NULL DEFAULT 'PENDING',
    status          text NOT NULL DEFAULT 'PENDING_SAFETY',
    approved_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    approved_at     timestamptz,
    client_event_id text UNIQUE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT surplus_batches_batch_code_key UNIQUE (batch_code),
    CONSTRAINT surplus_batches_quantity_check CHECK (quantity_kg > 0),
    CONSTRAINT surplus_batches_expiry_check CHECK (expiry_at > prepared_at)
);

CREATE TABLE matches (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id        uuid NOT NULL REFERENCES surplus_batches(id) ON DELETE CASCADE,
    recipient_id    uuid NOT NULL REFERENCES recipients(id) ON DELETE RESTRICT,
    score           numeric(6,2) NOT NULL DEFAULT 0,
    score_breakdown jsonb,
    reasons         text[],
    status          text NOT NULL DEFAULT 'OFFERED',
    offered_at      timestamptz NOT NULL DEFAULT now(),
    responded_at    timestamptz,
    expires_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT matches_batch_recipient_key UNIQUE (batch_id, recipient_id)
);

CREATE TABLE routes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id    uuid REFERENCES surplus_batches(id) ON DELETE CASCADE,
    driver_id   uuid,
    distance_km numeric(10,2),
    eta_minutes integer,
    solver      text,
    status      text NOT NULL DEFAULT 'planned',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE route_stops (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id     uuid NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    recipient_id uuid REFERENCES recipients(id) ON DELETE SET NULL,
    batch_id     uuid REFERENCES surplus_batches(id) ON DELETE SET NULL,
    sequence     integer NOT NULL,
    eta          timestamptz,
    address      text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT route_stops_sequence_key UNIQUE (route_id, sequence)
);

CREATE TRIGGER users_updated_at      BEFORE UPDATE ON users          FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER kitchens_updated_at   BEFORE UPDATE ON kitchens       FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER recipients_updated_at BEFORE UPDATE ON recipients     FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER surplus_updated_at    BEFORE UPDATE ON surplus_batches FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER matches_updated_at    BEFORE UPDATE ON matches        FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER routes_updated_at     BEFORE UPDATE ON routes         FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER waste_updated_at      BEFORE UPDATE ON waste          FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER meals_updated_at      BEFORE UPDATE ON meals          FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER attendance_updated_at BEFORE UPDATE ON attendance     FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER production_updated_at BEFORE UPDATE ON production     FOR EACH ROW EXECUTE FUNCTION set_updated_at();
