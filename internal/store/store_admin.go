package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"gomon/internal/model"
)

// ---- users & sessions ----

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, username, hash string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO users (username, password_hash) VALUES ($1,$2)`, username, hash)
	return err
}

func (s *Store) SetPassword(ctx context.Context, username, hash string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE username=$1`, username, hash)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) PasswordHash(ctx context.Context, username string) (string, error) {
	var h string
	err := s.DB.QueryRow(ctx, `SELECT password_hash FROM users WHERE username=$1`, username).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return h, err
}

func (s *Store) CreateSession(ctx context.Context, tokenHash, username, method string, expires time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO sessions (token_hash, username, auth_method, expires_at) VALUES ($1,$2,$3,$4)`,
		tokenHash, username, method, expires)
	return err
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (username, method string, err error) {
	err = s.DB.QueryRow(ctx, `SELECT username, auth_method FROM sessions WHERE token_hash=$1 AND expires_at > now()`, tokenHash).
		Scan(&username, &method)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

// ---- API keys ----

type APIKey struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"createdAt"`
	LastUsed  *time.Time `json:"lastUsed"`
}

func (s *Store) CreateAPIKey(ctx context.Context, name, hash string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO api_keys (name, key_hash) VALUES ($1,$2) RETURNING id`, name, hash).Scan(&id)
	return id, err
}

func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, name, created_at, last_used FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (APIKey, error) {
		var k APIKey
		err := r.Scan(&k.ID, &k.Name, &k.CreatedAt, &k.LastUsed)
		return k, err
	})
}

func (s *Store) DeleteAPIKey(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM api_keys WHERE id=$1`, id)
	return err
}

func (s *Store) CheckAPIKey(ctx context.Context, hash string) (string, error) {
	var name string
	err := s.DB.QueryRow(ctx, `UPDATE api_keys SET last_used=now() WHERE key_hash=$1 RETURNING name`, hash).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// ---- SNMP targets ----

const snmpCols = `id, host, address, port, version, community, v3, interval_seconds, poll_interfaces, oids, enabled, created_at`

func scanSNMP(r pgx.Row) (*model.SNMPTarget, error) {
	var t model.SNMPTarget
	var v3, oids []byte
	err := r.Scan(&t.ID, &t.Host, &t.Address, &t.Port, &t.Version, &t.Community, &v3, &t.IntervalSeconds,
		&t.PollInterfaces, &oids, &t.Enabled, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(v3, &t.V3)
	_ = json.Unmarshal(oids, &t.OIDs)
	if t.OIDs == nil {
		t.OIDs = []model.SNMPOID{}
	}
	return &t, nil
}

func (s *Store) ListSNMPTargets(ctx context.Context) ([]*model.SNMPTarget, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+snmpCols+` FROM snmp_targets ORDER BY host, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.SNMPTarget
	for rows.Next() {
		t, err := scanSNMP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetSNMPTarget(ctx context.Context, id int64) (*model.SNMPTarget, error) {
	return scanSNMP(s.DB.QueryRow(ctx, `SELECT `+snmpCols+` FROM snmp_targets WHERE id=$1`, id))
}

func (s *Store) SaveSNMPTarget(ctx context.Context, t *model.SNMPTarget) error {
	v3, _ := json.Marshal(t.V3)
	if t.OIDs == nil {
		t.OIDs = []model.SNMPOID{}
	}
	oids, _ := json.Marshal(t.OIDs)
	if t.ID == 0 {
		return s.DB.QueryRow(ctx, `INSERT INTO snmp_targets (host, address, port, version, community, v3, interval_seconds,
			poll_interfaces, oids, enabled) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id, created_at`,
			t.Host, t.Address, t.Port, t.Version, t.Community, v3, t.IntervalSeconds, t.PollInterfaces, oids, t.Enabled).
			Scan(&t.ID, &t.CreatedAt)
	}
	_, err := s.DB.Exec(ctx, `UPDATE snmp_targets SET host=$2, address=$3, port=$4, version=$5, community=$6, v3=$7,
		interval_seconds=$8, poll_interfaces=$9, oids=$10, enabled=$11 WHERE id=$1`,
		t.ID, t.Host, t.Address, t.Port, t.Version, t.Community, v3, t.IntervalSeconds, t.PollInterfaces, oids, t.Enabled)
	return err
}

func (s *Store) DeleteSNMPTarget(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM snmp_targets WHERE id=$1`, id)
	return err
}

// ---- alerting ----

func (s *Store) ListAlertChannels(ctx context.Context) ([]*model.AlertChannel, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, name, type, config, enabled FROM alert_channels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AlertChannel
	for rows.Next() {
		var c model.AlertChannel
		var cfg []byte
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &cfg, &c.Enabled); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(cfg, &c.Config)
		if c.Config == nil {
			c.Config = map[string]string{}
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *Store) SaveAlertChannel(ctx context.Context, c *model.AlertChannel) error {
	cfg, _ := json.Marshal(c.Config)
	if c.ID == 0 {
		return s.DB.QueryRow(ctx, `INSERT INTO alert_channels (name, type, config, enabled) VALUES ($1,$2,$3,$4) RETURNING id`,
			c.Name, c.Type, cfg, c.Enabled).Scan(&c.ID)
	}
	_, err := s.DB.Exec(ctx, `UPDATE alert_channels SET name=$2, type=$3, config=$4, enabled=$5 WHERE id=$1`,
		c.ID, c.Name, c.Type, cfg, c.Enabled)
	return err
}

func (s *Store) DeleteAlertChannel(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM alert_channels WHERE id=$1`, id)
	return err
}

func (s *Store) ListAlertRules(ctx context.Context) ([]*model.AlertRule, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, name, host_pattern, test_pattern, exclude_hosts, exclude_tests, min_color,
		delay_minutes, repeat_minutes, notify_recovery, channel_ids, enabled FROM alert_rules ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AlertRule
	for rows.Next() {
		var r model.AlertRule
		if err := rows.Scan(&r.ID, &r.Name, &r.HostPattern, &r.TestPattern, &r.ExcludeHosts, &r.ExcludeTests, &r.MinColor,
			&r.DelayMinutes, &r.RepeatMinutes, &r.NotifyRecovery, &r.ChannelIDs, &r.Enabled); err != nil {
			return nil, err
		}
		if r.ChannelIDs == nil {
			r.ChannelIDs = []int64{}
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *Store) SaveAlertRule(ctx context.Context, r *model.AlertRule) error {
	if r.ChannelIDs == nil {
		r.ChannelIDs = []int64{}
	}
	if r.ID == 0 {
		return s.DB.QueryRow(ctx, `INSERT INTO alert_rules (name, host_pattern, test_pattern, exclude_hosts, exclude_tests,
			min_color, delay_minutes, repeat_minutes, notify_recovery, channel_ids, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			r.Name, r.HostPattern, r.TestPattern, r.ExcludeHosts, r.ExcludeTests, r.MinColor, r.DelayMinutes,
			r.RepeatMinutes, r.NotifyRecovery, r.ChannelIDs, r.Enabled).Scan(&r.ID)
	}
	_, err := s.DB.Exec(ctx, `UPDATE alert_rules SET name=$2, host_pattern=$3, test_pattern=$4, exclude_hosts=$5,
		exclude_tests=$6, min_color=$7, delay_minutes=$8, repeat_minutes=$9, notify_recovery=$10, channel_ids=$11,
		enabled=$12 WHERE id=$1`,
		r.ID, r.Name, r.HostPattern, r.TestPattern, r.ExcludeHosts, r.ExcludeTests, r.MinColor, r.DelayMinutes,
		r.RepeatMinutes, r.NotifyRecovery, r.ChannelIDs, r.Enabled)
	return err
}

func (s *Store) DeleteAlertRule(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM alert_rules WHERE id=$1`, id)
	return err
}
