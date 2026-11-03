-- A notification that could not be delivered in time is abandoned, not
-- delivered. See internal/store/doc.go.
ALTER TABLE notifications ADD COLUMN abandoned_ms INTEGER;
