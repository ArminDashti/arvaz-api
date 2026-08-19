package store

import (
	"context"
	"embed"
	"errors"
	"time"

	"github.com/ArminDashti/arvaz-api/internal/asn"
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
	ISP                  string `json:"isp"`
	DownloadBytes        uint64 `json:"downloadBytes"`
	UploadBytes          uint64 `json:"uploadBytes"`
	UsageDurationSeconds int64  `json:"usageDurationSeconds"`
}

type SessionLog struct {
	Username        string     `json:"username,omitempty"`
	DownloadBytes   uint64     `json:"downloadBytes"`
	UploadBytes     uint64     `json:"uploadBytes"`
	ClientIP        string     `json:"ip"`
	ISP             string     `json:"isp"`
	DurationSeconds int64      `json:"durationSeconds"`
	ConnectedAt     time.Time  `json:"connectedAt"`
	DisconnectedAt  *time.Time `json:"disconnectedAt,omitempty"`
	IspLogo         string     `json:"ispLogo,omitempty"`
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
	for _, name := range []string{
		"migrations/001_softether.sql",
		"migrations/002_users.sql",
		"migrations/003_swap_traffic_polarity.sql",
		"migrations/004_softether_ip_index.sql",
	} {
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
			key = sess.Username + "|" + sess.SessionName
		}
		onlineKeys = append(onlineKeys, key)

		isp := asn.OrgName(sess.LastISP)
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
		`, sess.Username, sess.ClientIP, nullIfEmpty(isp), int64(sess.DownloadBytes), int64(sess.UploadBytes),
			connected, sess.SessionDurationSeconds, key)
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

	_, err = tx.Exec(ctx, `
		INSERT INTO softether_user_stats (
			username, client_ip, asn, download_bytes, upload_bytes, usage_duration_seconds, updated_at
		)
		SELECT
			username,
			COALESCE((ARRAY_AGG(client_ip ORDER BY connected_at DESC) FILTER (WHERE COALESCE(client_ip, '') <> ''))[1], ''),
			(ARRAY_AGG(asn ORDER BY connected_at DESC) FILTER (WHERE COALESCE(asn, '') <> ''))[1],
			COALESCE(SUM(download_bytes), 0),
			COALESCE(SUM(upload_bytes), 0),
			COALESCE(SUM(duration_seconds), 0),
			NOW()
		FROM softether_sessions
		GROUP BY username
		ON CONFLICT (username) DO UPDATE SET
			client_ip = CASE WHEN EXCLUDED.client_ip <> '' THEN EXCLUDED.client_ip ELSE softether_user_stats.client_ip END,
			asn = COALESCE(EXCLUDED.asn, softether_user_stats.asn),
			download_bytes = EXCLUDED.download_bytes,
			upload_bytes = EXCLUDED.upload_bytes,
			usage_duration_seconds = EXCLUDED.usage_duration_seconds,
			updated_at = NOW()
	`)
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
		var rawISP string
		if err := rows.Scan(&u.Username, &u.ClientIP, &rawISP, &u.DownloadBytes, &u.UploadBytes, &u.UsageDurationSeconds); err != nil {
			return nil, err
		}
		u.ISP = asn.OrgName(rawISP)
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

func (s *Store) ListSessionsByUsername(ctx context.Context, username string) ([]SessionLog, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(download_bytes, 0), COALESCE(upload_bytes, 0), COALESCE(client_ip, ''), COALESCE(asn, ''),
		       duration_seconds, connected_at, disconnected_at
		FROM softether_sessions
		WHERE username = $1
		ORDER BY connected_at DESC
	`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionLog{}
	for rows.Next() {
		var row SessionLog
		var rawISP string
		if err := rows.Scan(&row.DownloadBytes, &row.UploadBytes, &row.ClientIP, &rawISP, &row.DurationSeconds, &row.ConnectedAt, &row.DisconnectedAt); err != nil {
			return nil, err
		}
		row.ISP = asn.OrgName(rawISP)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ListSessionsByIP(ctx context.Context, ip string) ([]SessionLog, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT username, COALESCE(download_bytes, 0), COALESCE(upload_bytes, 0), COALESCE(client_ip, ''), COALESCE(asn, ''),
		       duration_seconds, connected_at, disconnected_at
		FROM softether_sessions
		WHERE client_ip = $1
		ORDER BY connected_at DESC
	`, ip)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionLog{}
	for rows.Next() {
		var row SessionLog
		var rawISP string
		if err := rows.Scan(&row.Username, &row.DownloadBytes, &row.UploadBytes, &row.ClientIP, &rawISP, &row.DurationSeconds, &row.ConnectedAt, &row.DisconnectedAt); err != nil {
			return nil, err
		}
		row.ISP = asn.OrgName(rawISP)
		out = append(out, row)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
