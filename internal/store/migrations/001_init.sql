CREATE TABLE hosts (
    name        TEXT PRIMARY KEY,
    ip          TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    page        TEXT NOT NULL DEFAULT '',
    os          TEXT NOT NULL DEFAULT '',
    class       TEXT NOT NULL DEFAULT '',
    notes       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE statuses (
    host           TEXT NOT NULL,
    test           TEXT NOT NULL,
    color          SMALLINT NOT NULL,
    real_color     SMALLINT NOT NULL,
    summary        TEXT NOT NULL DEFAULT '',
    message        TEXT NOT NULL DEFAULT '',
    last_change    TIMESTAMPTZ NOT NULL,
    last_update    TIMESTAMPTZ NOT NULL,
    valid_until    TIMESTAMPTZ NOT NULL,
    sender         TEXT NOT NULL DEFAULT '',
    transport      TEXT NOT NULL DEFAULT '',
    grp            TEXT NOT NULL DEFAULT '',
    cookie         BIGINT NOT NULL DEFAULT 0,
    ack_until      TIMESTAMPTZ,
    ack_msg        TEXT NOT NULL DEFAULT '',
    ack_by         TEXT NOT NULL DEFAULT '',
    disabled_until TIMESTAMPTZ,
    disable_msg    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (host, test)
);

CREATE TABLE status_history (
    id         BIGSERIAL PRIMARY KEY,
    host       TEXT NOT NULL,
    test       TEXT NOT NULL,
    ts         TIMESTAMPTZ NOT NULL,
    color      SMALLINT NOT NULL,
    prev_color SMALLINT NOT NULL,
    summary    TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX status_history_host_test_ts ON status_history (host, test, ts DESC);
CREATE INDEX status_history_ts ON status_history (ts DESC);

CREATE TABLE metrics (
    host  TEXT NOT NULL,
    test  TEXT NOT NULL,
    name  TEXT NOT NULL,
    ts    TIMESTAMPTZ NOT NULL,
    value DOUBLE PRECISION NOT NULL
);
CREATE INDEX metrics_lookup ON metrics (host, test, name, ts);
CREATE INDEX metrics_ts_brin ON metrics USING BRIN (ts);

CREATE TABLE client_data (
    host      TEXT NOT NULL,
    collector TEXT NOT NULL DEFAULT '',
    ts        TIMESTAMPTZ NOT NULL,
    os        TEXT NOT NULL DEFAULT '',
    class     TEXT NOT NULL DEFAULT '',
    sender    TEXT NOT NULL DEFAULT '',
    transport TEXT NOT NULL DEFAULT '',
    data      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (host, collector)
);

CREATE TABLE config_files (
    name       TEXT PRIMARY KEY,
    content    TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash  TEXT PRIMARY KEY,
    username    TEXT NOT NULL,
    auth_method TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE snmp_targets (
    id               BIGSERIAL PRIMARY KEY,
    host             TEXT NOT NULL,
    address          TEXT NOT NULL,
    port             INT NOT NULL DEFAULT 161,
    version          TEXT NOT NULL DEFAULT '2c',
    community        TEXT NOT NULL DEFAULT '',
    v3               JSONB NOT NULL DEFAULT '{}',
    interval_seconds INT NOT NULL DEFAULT 300,
    poll_interfaces  BOOLEAN NOT NULL DEFAULT true,
    oids             JSONB NOT NULL DEFAULT '[]',
    enabled          BOOLEAN NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE alert_channels (
    id      BIGSERIAL PRIMARY KEY,
    name    TEXT NOT NULL,
    type    TEXT NOT NULL,
    config  JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE alert_rules (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    host_pattern    TEXT NOT NULL DEFAULT '',
    test_pattern    TEXT NOT NULL DEFAULT '',
    exclude_hosts   TEXT NOT NULL DEFAULT '',
    exclude_tests   TEXT NOT NULL DEFAULT '',
    min_color       TEXT NOT NULL DEFAULT 'red',
    delay_minutes   INT NOT NULL DEFAULT 0,
    repeat_minutes  INT NOT NULL DEFAULT 60,
    notify_recovery BOOLEAN NOT NULL DEFAULT true,
    channel_ids     BIGINT[] NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE api_keys (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    key_hash   TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used  TIMESTAMPTZ
);
