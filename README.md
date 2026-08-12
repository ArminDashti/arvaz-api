# Arvaz API

Host monitoring backend for Irancell-T3: Gin, PostgreSQL, Docker, SoftEther, filesystem explorer.

## Stack

- Go + Gin
- PostgreSQL (pgx)
- gopsutil (CPU, memory, disk, network)
- Docker CLI + SoftEther vpncmd via `docker exec`

## Local dev

```bash
docker compose up -d
cp .env.example .env
go run ./cmd/server
```

Default login (seeded): `armin` / `dopadopa1234`

API: `http://127.0.0.1:8090`

## Production (T3)

Native systemd install — **not** Docker. See `deploy/arvaz-api.service` and `deploy/env.example`.
