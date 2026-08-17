CREATE INDEX IF NOT EXISTS idx_softether_sessions_client_ip
    ON softether_sessions (client_ip);
