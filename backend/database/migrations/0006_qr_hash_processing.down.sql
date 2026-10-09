DROP TABLE IF EXISTS processing_metrics;
DROP TRIGGER IF EXISTS qr_events_immutable_trg ON qr_events;
DROP FUNCTION IF EXISTS qr_events_immutable();
DROP TABLE IF EXISTS qr_events;
