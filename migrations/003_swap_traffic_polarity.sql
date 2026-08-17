CREATE TABLE IF NOT EXISTS schema_migrations (
    name TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM schema_migrations WHERE name = '003_swap_traffic_polarity'
    ) THEN
        UPDATE softether_sessions
        SET download_bytes = upload_bytes,
            upload_bytes = download_bytes;
        UPDATE softether_user_stats
        SET download_bytes = upload_bytes,
            upload_bytes = download_bytes;
        INSERT INTO schema_migrations (name) VALUES ('003_swap_traffic_polarity');
    END IF;
END $$;
