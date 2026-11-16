// Package store persists gomon state in PostgreSQL.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gomon/internal/model"
	"gomon/internal/protocol"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var ErrNotFound = errors.New("not found")

type Store struct {
	DB *pgxpool.Pool
}

// Open connects to Postgres, retrying for up to a minute (the database
// container may still be starting), and applies migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if cfg.MaxConns < 10 {
		cfg.MaxConns = 20
	}
	var pool *pgxpool.Pool
	deadline := time.Now().Add(60 * time.Second)
	for {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				break
			}
			pool.Close()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connect to database: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	s := &Store{DB: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { s.DB.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.DB.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		base := strings.TrimPrefix(f, "migrations/")
		v, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %s", f)
		}
		var exists bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, v).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sql, _ := migrationsFS.ReadFile(f)
		tx, err := s.DB.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", base, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, v); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ---- hosts ----

const hostCols = `name, ip, tags, description, page, grp, os, class, notes, created_at, updated_at`

func scanHost(row pgx.Row) (*model.Host, error) {
	var h model.Host
	err := row.Scan(&h.Name, &h.IP, &h.Tags, &h.Description, &h.Page, &h.Group, &h.OS, &h.Class, &h.Notes, &h.CreatedAt, &h.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &h, err
}

func (s *Store) ListHosts(ctx context.Context) ([]*model.Host, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+hostCols+` FROM hosts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) GetHost(ctx context.Context, name string) (*model.Host, error) {
	return scanHost(s.DB.QueryRow(ctx, `SELECT `+hostCols+` FROM hosts WHERE name=$1`, name))
}

func (s *Store) UpsertHost(ctx context.Context, h *model.Host) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO hosts (name, ip, tags, description, page, os, class, notes, grp)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (name) DO UPDATE SET ip=$2, tags=$3, description=$4, page=$5, os=$6, class=$7, notes=$8, grp=$9, updated_at=now()`,
		h.Name, h.IP, h.Tags, h.Description, h.Page, h.OS, h.Class, h.Notes, h.Group)
	return err
}

// EnsureHost creates a host if it does not exist yet.
func (s *Store) EnsureHost(ctx context.Context, name, ip string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO hosts (name, ip) VALUES ($1,$2) ON CONFLICT (name) DO NOTHING`, name, ip)
	return err
}

func (s *Store) SetHostOS(ctx context.Context, name, os, class string) error {
	_, err := s.DB.Exec(ctx, `UPDATE hosts SET os=$2, class=CASE WHEN $3='' THEN class ELSE $3 END WHERE name=$1 AND (os<>$2 OR ($3<>'' AND class<>$3))`, name, os, class)
	return err
}

func (s *Store) DeleteHost(ctx context.Context, name string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, q := range []string{
		`DELETE FROM hosts WHERE name=$1`,
		`DELETE FROM statuses WHERE host=$1`,
		`DELETE FROM status_history WHERE host=$1`,
		`DELETE FROM metrics WHERE host=$1`,
		`DELETE FROM client_data WHERE host=$1`,
		`DELETE FROM snmp_targets WHERE host=$1`,
	} {
		if _, err := tx.Exec(ctx, q, name); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RenameHost(ctx context.Context, from, to string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, q := range []string{
		`UPDATE hosts SET name=$2 WHERE name=$1`,
		`UPDATE statuses SET host=$2 WHERE host=$1`,
		`UPDATE status_history SET host=$2 WHERE host=$1`,
		`UPDATE metrics SET host=$2 WHERE host=$1`,
		`UPDATE client_data SET host=$2 WHERE host=$1`,
		`UPDATE snmp_targets SET host=$2 WHERE host=$1`,
	} {
		if _, err := tx.Exec(ctx, q, from, to); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ---- statuses ----

func (s *Store) LoadStatuses(ctx context.Context) ([]*model.Status, error) {
	rows, err := s.DB.Query(ctx, `SELECT host, test, color, real_color, summary, message, last_change, last_update,
		valid_until, sender, transport, grp, cookie, ack_until, ack_msg, ack_by, disabled_until, disable_msg FROM statuses`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Status
	for rows.Next() {
		var st model.Status
		var c, rc int16
		if err := rows.Scan(&st.Host, &st.Test, &c, &rc, &st.Summary, &st.Message, &st.LastChange, &st.LastUpdate,
			&st.ValidUntil, &st.Sender, &st.Transport, &st.Group, &st.Cookie, &st.AckUntil, &st.AckMsg, &st.AckBy,
			&st.DisabledUntil, &st.DisableMsg); err != nil {
			return nil, err
		}
		st.Color, st.RealColor = protocol.Color(c), protocol.Color(rc)
		out = append(out, &st)
	}
	return out, rows.Err()
}

func (s *Store) SaveStatus(ctx context.Context, st *model.Status) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO statuses (host, test, color, real_color, summary, message, last_change, last_update, valid_until,
			sender, transport, grp, cookie, ack_until, ack_msg, ack_by, disabled_until, disable_msg)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (host, test) DO UPDATE SET color=$3, real_color=$4, summary=$5, message=$6, last_change=$7,
			last_update=$8, valid_until=$9, sender=$10, transport=$11, grp=$12, cookie=$13, ack_until=$14,
			ack_msg=$15, ack_by=$16, disabled_until=$17, disable_msg=$18`,
		st.Host, st.Test, int16(st.Color), int16(st.RealColor), st.Summary, st.Message, st.LastChange, st.LastUpdate,
		st.ValidUntil, st.Sender, st.Transport, st.Group, st.Cookie, st.AckUntil, st.AckMsg, st.AckBy,
		st.DisabledUntil, st.DisableMsg)
	return err
}

func (s *Store) DeleteStatus(ctx context.Context, host, test string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM statuses WHERE host=$1 AND test=$2`, host, test)
	return err
}

// ---- history ----

func (s *Store) AddHistory(ctx context.Context, ev *model.HistoryEvent) error {
	return s.DB.QueryRow(ctx, `INSERT INTO status_history (host, test, ts, color, prev_color, summary, message)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		ev.Host, ev.Test, ev.Time, int16(ev.Color), int16(ev.PrevColor), ev.Summary, ev.Message).Scan(&ev.ID)
}

type HistoryQuery struct {
	Host   string
	Test   string
	Colors []protocol.Color
	Before int64 // id cursor
	Limit  int
}

func (s *Store) ListHistory(ctx context.Context, q HistoryQuery) ([]*model.HistoryEvent, error) {
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 200
	}
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if q.Host != "" {
		add("host=$%d", q.Host)
	}
	if q.Test != "" {
		add("test=$%d", q.Test)
	}
	if q.Before > 0 {
		add("id<$%d", q.Before)
	}
	if len(q.Colors) > 0 {
		cs := make([]int16, len(q.Colors))
		for i, c := range q.Colors {
			cs[i] = int16(c)
		}
		add("color=ANY($%d)", cs)
	}
	sql := `SELECT id, host, test, ts, color, prev_color, summary FROM status_history`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, q.Limit)
	sql += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.HistoryEvent
	for rows.Next() {
		var e model.HistoryEvent
		var c, pc int16
		if err := rows.Scan(&e.ID, &e.Host, &e.Test, &e.Time, &c, &pc, &e.Summary); err != nil {
			return nil, err
		}
		e.Color, e.PrevColor = protocol.Color(c), protocol.Color(pc)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Store) GetHistoryEvent(ctx context.Context, id int64) (*model.HistoryEvent, error) {
	var e model.HistoryEvent
	var c, pc int16
	err := s.DB.QueryRow(ctx, `SELECT id, host, test, ts, color, prev_color, summary, message FROM status_history WHERE id=$1`, id).
		Scan(&e.ID, &e.Host, &e.Test, &e.Time, &c, &pc, &e.Summary, &e.Message)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	e.Color, e.PrevColor = protocol.Color(c), protocol.Color(pc)
	return &e, err
}

// ---- metrics ----

func (s *Store) InsertMetrics(ctx context.Context, samples []model.MetricSample) error {
	if len(samples) == 0 {
		return nil
	}
	_, err := s.DB.CopyFrom(ctx, pgx.Identifier{"metrics"}, []string{"host", "test", "name", "ts", "value"},
		pgx.CopyFromSlice(len(samples), func(i int) ([]any, error) {
			m := samples[i]
			return []any{m.Host, m.Test, m.Name, m.Time, m.Value}, nil
		}))
	return err
}

type MetricSeries struct {
	Test   string       `json:"test"`
	Name   string       `json:"name"`
	Points [][2]float64 `json:"points"` // [unix ms, value]
}

// QueryMetrics returns series for a host (optionally one test), averaged
// into buckets of step.
func (s *Store) QueryMetrics(ctx context.Context, host, test string, from, to time.Time, step time.Duration) ([]*MetricSeries, error) {
	args := []any{host, from, to, fmt.Sprintf("%d seconds", int(step.Seconds()))}
	cond := ""
	if test != "" {
		args = append(args, test)
		cond = " AND test=$5"
	}
	rows, err := s.DB.Query(ctx, `
		SELECT test, name, date_bin($4::interval, ts, TIMESTAMPTZ '2000-01-01') AS b, avg(value)
		FROM metrics WHERE host=$1 AND ts >= $2 AND ts <= $3`+cond+`
		GROUP BY test, name, b ORDER BY test, name, b`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MetricSeries
	var cur *MetricSeries
	for rows.Next() {
		var t, n string
		var b time.Time
		var v float64
		if err := rows.Scan(&t, &n, &b, &v); err != nil {
			return nil, err
		}
		if cur == nil || cur.Test != t || cur.Name != n {
			cur = &MetricSeries{Test: t, Name: n}
			out = append(out, cur)
		}
		cur.Points = append(cur.Points, [2]float64{float64(b.UnixMilli()), v})
	}
	return out, rows.Err()
}

// MetricTests lists the tests that have metrics for a host.
func (s *Store) MetricTests(ctx context.Context, host string, since time.Time) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT test FROM metrics WHERE host=$1 AND ts >= $2 ORDER BY test`, host, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ---- client data ----

func (s *Store) SaveClientData(ctx context.Context, c *model.ClientData) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO client_data (host, collector, ts, os, class, sender, transport, data) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (host, collector) DO UPDATE SET ts=$3, os=$4, class=$5, sender=$6, transport=$7, data=$8`,
		c.Host, c.Collector, c.Time, c.OS, c.Class, c.Sender, c.Transport, c.Data)
	return err
}

func (s *Store) GetClientData(ctx context.Context, host string) ([]*model.ClientData, error) {
	rows, err := s.DB.Query(ctx, `SELECT host, collector, ts, os, class, sender, transport, data FROM client_data WHERE host=$1 ORDER BY collector`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ClientData
	for rows.Next() {
		var c model.ClientData
		if err := rows.Scan(&c.Host, &c.Collector, &c.Time, &c.OS, &c.Class, &c.Sender, &c.Transport, &c.Data); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// ---- config files (analysis.cfg, client-local.cfg) ----

func (s *Store) GetConfigFile(ctx context.Context, name string) (string, time.Time, error) {
	var content string
	var ts time.Time
	err := s.DB.QueryRow(ctx, `SELECT content, updated_at FROM config_files WHERE name=$1`, name).Scan(&content, &ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, ErrNotFound
	}
	return content, ts, err
}

func (s *Store) PutConfigFile(ctx context.Context, name, content string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO config_files (name, content) VALUES ($1,$2)
		ON CONFLICT (name) DO UPDATE SET content=$2, updated_at=now()`, name, content)
	return err
}

// ---- retention ----

func (s *Store) Prune(ctx context.Context, metricsBefore, historyBefore time.Time) error {
	if _, err := s.DB.Exec(ctx, `DELETE FROM metrics WHERE ts < $1`, metricsBefore); err != nil {
		return err
	}
	if _, err := s.DB.Exec(ctx, `DELETE FROM status_history WHERE ts < $1`, historyBefore); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}

// ---- page titles ----

func (s *Store) PageTitles(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT path, title FROM page_titles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, t string
		if err := rows.Scan(&p, &t); err != nil {
			return nil, err
		}
		out[p] = t
	}
	return out, rows.Err()
}

func (s *Store) SetPageTitles(ctx context.Context, titles map[string]string) error {
	for p, t := range titles {
		if _, err := s.DB.Exec(ctx, `INSERT INTO page_titles (path, title) VALUES ($1,$2)
			ON CONFLICT (path) DO UPDATE SET title=$2`, p, t); err != nil {
			return err
		}
	}
	return nil
}
