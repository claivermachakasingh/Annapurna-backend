DROP TABLE IF EXISTS device_tokens;
DROP TABLE IF EXISTS refresh_tokens;
ALTER TABLE matches         DROP COLUMN IF EXISTS client_ts;
ALTER TABLE qr_events       DROP COLUMN IF EXISTS client_ts;
ALTER TABLE qr_events       DROP COLUMN IF EXISTS client_event_id;
ALTER TABLE surplus_batches DROP COLUMN IF EXISTS client_event_id;
ALTER TABLE waste           DROP COLUMN IF EXISTS client_event_id;
