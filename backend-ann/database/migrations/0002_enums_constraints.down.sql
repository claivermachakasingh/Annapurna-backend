ALTER TABLE matches          DROP CONSTRAINT IF EXISTS matches_score_check, DROP CONSTRAINT IF EXISTS matches_status_check;
ALTER TABLE surplus_batches  DROP CONSTRAINT IF EXISTS surplus_safety_check, DROP CONSTRAINT IF EXISTS surplus_status_check;
ALTER TABLE waste            DROP CONSTRAINT IF EXISTS waste_cause_check, DROP CONSTRAINT IF EXISTS waste_quantity_check;
ALTER TABLE attendance       DROP CONSTRAINT IF EXISTS attendance_head_check, DROP CONSTRAINT IF EXISTS attendance_actual_check, DROP CONSTRAINT IF EXISTS attendance_expected_check;
ALTER TABLE meals            DROP CONSTRAINT IF EXISTS meals_type_check;
ALTER TABLE recipients       DROP CONSTRAINT IF EXISTS recipients_window_check, DROP CONSTRAINT IF EXISTS recipients_capacity_check, DROP CONSTRAINT IF EXISTS recipients_type_check;
ALTER TABLE kitchens         DROP CONSTRAINT IF EXISTS kitchens_type_check;
ALTER TABLE users            DROP CONSTRAINT IF EXISTS users_email_check, DROP CONSTRAINT IF EXISTS users_role_check;
