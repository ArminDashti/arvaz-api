package store

import (
	"context"
	"embed"
	"errors"
	"time"

	"github.com/ArminDashti/arvaz-api/internal/softether"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	pool *pgxpool.Pool
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
}

type UserStat struct {
	Username             string `json:"username"`
	ClientIP             string `json:"clientIp"`
	ASN                  string `json:"asn"`
	DownloadBytes        uint64 `json:"downloadBytes"`
	UploadBytes          uint64 `json:"uploadBytes"`
	UsageDurationSeconds int64  `json:"usageDurationSeconds"`
}

func Connect(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	s := &Store{pool: pool}
	if err := s.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Migrate(ctx context.Context) error {
	for _, name := range []string{"migrations/001_softether.sql", "migrations/002_users.sql"} {
		sqlBytes, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, string(sqlBytes)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO users (username, password_hash) VALUES ($1, $2)`, username, passwordHash)
	return err
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `SELECT id, username, password_hash FROM users WHERE username = $1`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) SyncOnlineSessions(ctx context.Context, sessions []softether.OnlineSession) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	onlineKeys := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		key := sess.SessionKey
		if key == "" {
			key = sess.Username + "|" + sess.ClientIP + "|" + sess.SessionName
		}
		onlineKeys = append(onlineKeys, key)

		asn := sess.LastASN
		connected := time.Now().UTC()
		if sess.ConnectedAt != nil {
			connected = *sess.ConnectedAt
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO softether_sessions (
				username, client_ip, asn, download_bytes, upload_bytes,
				connected_at, disconnected_at, duration_seconds, session_key
			) VALUES ($1,$2,$3,$4,$5,$6,NULL,$7,$8)
			ON CONFLICT (session_key) DO UPDATE SET
				download_bytes = EXCLUDED.download_bytes,
				upload_bytes = EXCLUDED.upload_bytes,
				duration_seconds = EXCLUDED.duration_seconds,
				client_ip = EXCLUDED.client_ip,
				asn = COALESCE(EXCLUDED.asn, softether_sessions.asn)
		`, sess.Username, sess.ClientIP, nullIfEmpty(asn), int64(sess.DownloadBytes), int64(sess.UploadBytes),
			connected, sess.SessionDurationSeconds, key)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO softether_user_stats (
				username, client_ip, asn, download_bytes, upload_bytes, usage_duration_seconds, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,NOW())
			ON CONFLICT (username) DO UPDATE SET
				client_ip = CASE WHEN EXCLUDED.client_ip <> '' THEN EXCLUDED.client_ip ELSE softether_user_stats.client_ip END,
				asn = COALESCE(EXCLUDED.asn, softether_user_stats.asn),
				download_bytes = GREATEST(softether_user_stats.download_bytes, EXCLUDED.download_bytes),
				upload_bytes = GREATEST(softether_user_stats.upload_bytes, EXCLUDED.upload_bytes),
				usage_duration_seconds = GREATEST(softether_user_stats.usage_duration_seconds, EXCLUDED.usage_duration_seconds),
				updated_at = NOW()
		`, sess.Username, sess.ClientIP, nullIfEmpty(asn), int64(sess.DownloadBytes), int64(sess.UploadBytes), sess.SessionDurationSeconds)
		if err != nil {
			return err
		}
	}

	if len(onlineKeys) == 0 {
		_, err = tx.Exec(ctx, `
			UPDATE softether_sessions
			SET disconnected_at = NOW(),
			    duration_seconds = EXTRACT(EPOCH FROM (NOW() - connected_at))::BIGINT
			WHERE disconnected_at IS NULL
		`)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE softether_sessions
			SET disconnected_at = NOW(),
			    duration_seconds = EXTRACT(EPOCH FROM (NOW() - connected_at))::BIGINT
			WHERE disconnected_at IS NULL
			  AND NOT (session_key = ANY($1))
		`, onlineKeys)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListUserStats(ctx context.Context) ([]UserStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT username, COALESCE(client_ip,''), COALESCE(asn,''), download_bytes, upload_bytes, usage_duration_seconds
		FROM softether_user_stats
		ORDER BY username
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserStat{}
	for rows.Next() {
		var u UserStat
		if err := rows.Scan(&u.Username, &u.ClientIP, &u.ASN, &u.DownloadBytes, &u.UploadBytes, &u.UsageDurationSeconds); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) GetUserStatMap(ctx context.Context) (map[string]UserStat, error) {
	stats, err := s.ListUserStats(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]UserStat, len(stats))
	for _, st := range stats {
		out[st.Username] = st
	}
	return out, nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
