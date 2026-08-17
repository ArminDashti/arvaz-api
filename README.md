# arvaz-api

Host ops API for Irancell-T3: Gin, PostgreSQL auth, Docker containers inventory, Mullvad controls via `docker exec`.

## Features

- JWT login (`armin` / configured default password)
- `GET /api/v1/docker/containers` — stack, CPU, memory, network name+IP, HAProxy URL, uptime, state
- Mullvad status, relay list, set relay, anti-censorship, ping, Ookla-compatible speedtest
- SoftEther live sessions, hub users, per-user session logs, and `GET /api/v1/softether/ip-sessions?ip=` history by client IP

## Safety

Never restarts SoftEther / Mullvad. Mullvad write actions are operator-initiated only.
