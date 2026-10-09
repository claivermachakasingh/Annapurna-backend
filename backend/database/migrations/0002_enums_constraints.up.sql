-- 0002 domain constraints. The frozen contract (08_API_CONTRACT) lists enums;
-- they are enforced as CHECK constraints (case-normalised with upper()) rather
-- than PG ENUM types so that offline replay of mixed-case legacy payloads
-- degrades to a clean 422 instead of a cast error.

ALTER TABLE users
    ADD CONSTRAINT users_role_check
        CHECK (upper(role) IN ('KITCHEN','NGO','LOGISTICS','ADMIN','SYSTEM')),
    ADD CONSTRAINT users_email_check CHECK (position('@' in email) > 1);

ALTER TABLE kitchens
    ADD CONSTRAINT kitchens_type_check
        CHECK (upper(type) IN ('HOSTEL','CORPORATE','HOSPITAL','CATERER','PROCESSING'));

ALTER TABLE recipients
    ADD CONSTRAINT recipients_type_check
        CHECK (upper(type) IN ('NGO','FOOD_BANK','SHELTER','COMMUNITY_KITCHEN','BUYER','FEED_FARM','COMPOST')),
    ADD CONSTRAINT recipients_capacity_check CHECK (capacity_kg >= 0),
    ADD CONSTRAINT recipients_window_check CHECK (pickup_window_end > pickup_window_start);

ALTER TABLE meals
    ADD CONSTRAINT meals_type_check CHECK (meal_type IN ('BREAKFAST','LUNCH','SNACK','DINNER'));

ALTER TABLE attendance
    ADD CONSTRAINT attendance_expected_check CHECK (expected_diners IS NULL OR expected_diners >= 0),
    ADD CONSTRAINT attendance_actual_check   CHECK (actual_diners IS NULL OR actual_diners >= 0),
    ADD CONSTRAINT attendance_head_check     CHECK (head_count IS NULL OR head_count >= 0);

ALTER TABLE waste
    ADD CONSTRAINT waste_quantity_check CHECK (quantity_kg >= 0),
    ADD CONSTRAINT waste_cause_check CHECK (
        cause IS NULL OR cause IN ('OVERPRODUCTION','LOW_ATTENDANCE','SPOILAGE','EXPIRY',
                                   'STORAGE_ISSUE','PREPARATION_ERROR','OTHER'));

ALTER TABLE surplus_batches
    ADD CONSTRAINT surplus_status_check CHECK (status IN (
        'PENDING_SAFETY','AVAILABLE','HOLD','DIVERTED','EXPIRED','MATCHED','IN_TRANSIT','DELIVERED')),
    ADD CONSTRAINT surplus_safety_check CHECK (safety_status IN (
        'PENDING','ELIGIBLE','HOLD','REJECTED'));

ALTER TABLE matches
    ADD CONSTRAINT matches_status_check CHECK (status IN ('OFFERED','ACCEPTED','DECLINED','EXPIRED')),
    ADD CONSTRAINT matches_score_check CHECK (score >= 0);
