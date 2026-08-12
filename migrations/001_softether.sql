CREATE TABLE IF NOT EXISTS softether_sessions (
    id              BIGSERIAL PRIMARY KEY,
    username        TEXT NOT NULL,
    client_ip       TEXT,
    asn             TEXT,
    download_bytes  BIGINT NOT NULL DEFAULT 0,
    upload_bytes    BIGINT NOT NULL DEFAULT 0,
    connected_at    TIMESTAMPTZ NOT NULL,
    disconnected_at TIMESTAMPTZ,
    duration_seconds BIGINT NOT NULL DEFAULT 0,
    session_key     TEXT NOT NULL UNIQUE
);

CREATE INDEX IF NOT EXISTS idx_softether_sessions_username
    ON softether_sessions (username);

CREATE INDEX IF NOT EXISTS idx_softether_sessions_connected
    ON softether_sessions (connected_at DESC);

CREATE TABLE IF NOT EXISTS softether_user_stats (
    username               TEXT PRIMARY KEY,
    client_ip              TEXT,
    asn                    TEXT,
    download_bytes         BIGINT NOT NULL DEFAULT 0,
    upload_bytes           BIGINT NOT NULL DEFAULT 0,
    usage_duration_seconds BIGINT NOT NULL DEFAULT 0,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
