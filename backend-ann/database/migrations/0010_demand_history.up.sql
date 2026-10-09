-- 0010 demand history datasets.
--
-- Kitchens upload their own historical footfall / order data (csv, tsv, txt or
-- xlsx) and predict demand from it. This table stores those rows verbatim so a
-- prediction can be re-run or audited, and so the model can be retrained
-- against the same inputs later.
--
-- No prediction logic lives here: the rows are data only.

CREATE TABLE demand_datasets (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kitchen_id    uuid NOT NULL REFERENCES kitchens(id) ON DELETE CASCADE,
    filename      text NOT NULL,
    content_type  text,
    row_count     integer NOT NULL DEFAULT 0,
    rejected_rows integer NOT NULL DEFAULT 0,
    columns_found text[] NOT NULL DEFAULT '{}',
    created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE demand_history (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id       uuid NOT NULL REFERENCES demand_datasets(id) ON DELETE CASCADE,
    kitchen_id       uuid NOT NULL REFERENCES kitchens(id) ON DELETE CASCADE,

    -- Observation date. When the upload omits it, the ingest date is used so a
    -- row is never silently dropped.
    observed_on      date NOT NULL,
    day_of_week      integer,
    meal_type        text,

    -- The two signals the kitchen actually records.
    footfall         integer,   -- headcount / attendance
    orders_count     integer,   -- orders placed (canteen / POS style)

    -- Outcome columns, when the file has them. Nullable because a footfall-only
    -- export is a legitimate upload.
    food_prepared_kg numeric(10,2),
    food_consumed_kg numeric(10,2),
    waste_kg         numeric(10,2),

    raw               jsonb NOT NULL DEFAULT '{}'::jsonb,
    row_index         integer NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT demand_history_footfall_check  CHECK (footfall IS NULL OR footfall >= 0),
    CONSTRAINT demand_history_orders_check   CHECK (orders_count IS NULL OR orders_count >= 0),
    CONSTRAINT demand_history_dow_check      CHECK (day_of_week IS NULL OR day_of_week BETWEEN 0 AND 6),
    CONSTRAINT demand_history_kitchen_dup    UNIQUE (dataset_id, row_index)
);

CREATE INDEX demand_datasets_kitchen_idx   ON demand_datasets (kitchen_id, created_at DESC);
CREATE INDEX demand_history_kitchen_date_idx ON demand_history (kitchen_id, observed_on DESC);
CREATE INDEX demand_history_dataset_idx      ON demand_history (dataset_id);

-- Only active, non-deleted kitchen rows matter for training.
CREATE OR REPLACE VIEW v_uploaded_history AS
SELECT
    h.observed_on,
    h.day_of_week,
    h.meal_type,
    h.footfall,
    h.orders_count,
    h.food_prepared_kg,
    h.food_consumed_kg,
    h.waste_kg,
    h.kitchen_id,
    h.dataset_id
FROM demand_history h
JOIN demand_datasets d ON d.id = h.dataset_id;