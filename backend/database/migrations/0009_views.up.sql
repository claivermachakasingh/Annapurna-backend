-- 0009 read models (04_DATABASE_SPEC §5). Kept in its own migration because
-- v_impact_summary depends on tables introduced in 0006/0007.

CREATE OR REPLACE VIEW v_training_set AS
SELECT
    m.date,
    m.kitchen_id,
    (EXTRACT(ISODOW FROM m.date)::int - 1) AS day_of_week,   -- ISO: 0 = Monday (D6)
    m.meal_type,
    m.menu,
    COALESCE(a.expected_diners, a.head_count) AS attendance,
    COALESCE(a.actual_diners, a.head_count)   AS actual_diners,
    p.prepared_qty,
    p.consumed_qty,
    COALESCE(sb.surplus_qty, 0) AS surplus_qty,
    COALESCE(w.waste_qty, 0)    AS waste_qty
FROM meals m
LEFT JOIN attendance a ON a.meal_id = m.id
LEFT JOIN production  p ON p.meal_id = m.id
LEFT JOIN (
    SELECT kitchen_id, prepared_at::date AS date, SUM(quantity_kg) AS surplus_qty
    FROM surplus_batches GROUP BY kitchen_id, prepared_at::date
) sb ON sb.kitchen_id = m.kitchen_id AND sb.date = m.date
LEFT JOIN (
    SELECT kitchen_id, recorded_at::date AS date, SUM(quantity_kg) AS waste_qty
    FROM waste GROUP BY kitchen_id, recorded_at::date
) w ON w.kitchen_id = m.kitchen_id AND w.date = m.date;

CREATE OR REPLACE VIEW v_daily_waste AS
SELECT
    kitchen_id,
    recorded_at::date AS date,
    COALESCE(cause, waste_type, 'OTHER') AS cause,
    SUM(quantity_kg) AS waste_kg,
    COUNT(*)         AS records
FROM waste
GROUP BY kitchen_id, recorded_at::date, COALESCE(cause, waste_type, 'OTHER');

CREATE OR REPLACE VIEW v_impact_summary AS
WITH delivered AS (
    SELECT kitchen_id, updated_at::date AS date, SUM(quantity_kg) AS kg
    FROM surplus_batches WHERE status = 'DELIVERED'
    GROUP BY kitchen_id, updated_at::date
), diverted AS (
    SELECT sb.kitchen_id, d.created_at::date AS date, SUM(COALESCE(d.quantity_kg, sb.quantity_kg)) AS kg
    FROM diversions d JOIN surplus_batches sb ON sb.id = d.batch_id
    GROUP BY sb.kitchen_id, d.created_at::date
)
SELECT
    COALESCE(dl.kitchen_id, dv.kitchen_id)                        AS kitchen_id,
    COALESCE(dl.date, dv.date)                                    AS date,
    COALESCE(dl.kg, 0)                                            AS redistributed_kg,
    COALESCE(dv.kg, 0)                                            AS diverted_kg,
    COALESCE(dl.kg, 0) + COALESCE(dv.kg, 0)                       AS waste_avoided_kg
FROM delivered dl
FULL OUTER JOIN diverted dv ON dv.kitchen_id = dl.kitchen_id AND dv.date = dl.date;
