// Package config loads gomon's configuration from a TOML file with
// environment variable overrides (GOMON_*).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	DataDir   string          `toml:"dataDir"`
	LogLevel  string          `toml:"logLevel"`
	HTTP      HTTPConfig      `toml:"http"`
	Database  DatabaseConfig  `toml:"database"`
	Xymon     XymonConfig     `toml:"xymon"`
	OIDC      OIDCConfig      `toml:"oidc"`
	Retention RetentionConfig `toml:"retention"`
	Network   NetworkConfig   `toml:"network"`
	SNMP      SNMPConfig      `toml:"snmp"`
	Metrics   MetricsConfig   `toml:"metrics"`
}

type HTTPConfig struct {
	Listen string `toml:"listen"`
	// BaseURL is the externally visible URL (used for OIDC redirects and
	// links in alert notifications), e.g. https://gomon.example.com
	BaseURL string `toml:"baseUrl"`
	// SecureCookies forces the Secure flag on session cookies. It is also
	// enabled automatically when BaseURL starts with https://.
	SecureCookies bool `toml:"secureCookies"`
	SessionHours  int  `toml:"sessionHours"`
}

type DatabaseConfig struct {
	DSN string `toml:"dsn"`
}

type XymonConfig struct {
	// Listen is the plaintext Xymon port (1984). Empty disables it.
	Listen string `toml:"listen"`
	// TLSListen is the Xymon-over-TLS port (1985), compatible with
	// upstream xymon's "xymons://" recipients. Empty disables it.
	TLSListen string `toml:"tlsListen"`
	// TLSCert/TLSKey: server certificate. If empty, gomon issues one from
	// its built-in CA (stored in dataDir/pki).
	TLSCert string `toml:"tlsCert"`
	TLSKey  string `toml:"tlsKey"`
	// TLSServerNames are extra SANs for the auto-issued server certificate.
	TLSServerNames []string `toml:"tlsServerNames"`
	// ClientCA verifies client certificates. Defaults to the built-in CA.
	ClientCA string `toml:"clientCa"`
	// RequireClientCert refuses TLS clients that present no certificate.
	RequireClientCert bool `toml:"requireClientCert"`
	// StatusSenders: networks allowed to report for any host. Clients with
	// a verified certificate may always report for the host it names.
	StatusSenders []string `toml:"statusSenders"`
	// AdminSenders: networks allowed to send drop/rename/disable/enable/ack.
	AdminSenders []string `toml:"adminSenders"`
	// AutoRegister creates hosts the first time they report (default on).
	AutoRegister bool `toml:"autoRegister"`
	// MaxMessageSize in bytes.
	MaxMessageSize int `toml:"maxMessageSize"`
	// DefaultLifetimeMinutes before a status goes purple.
	DefaultLifetimeMinutes int `toml:"defaultLifetimeMinutes"`
}

type OIDCConfig struct {
	Enabled             bool     `toml:"enabled"`
	Issuer              string   `toml:"issuer"`
	ClientID            string   `toml:"clientId"`
	ClientSecret        string   `toml:"clientSecret"`
	RedirectURL         string   `toml:"redirectUrl"`
	Scopes              []string `toml:"scopes"`
	DisableBuiltinLogin bool     `toml:"disableBuiltinLogin"`
	// AllowedGroups restricts login to members of these groups (empty: all).
	AllowedGroups []string `toml:"allowedGroups"`
	GroupsClaim   string   `toml:"groupsClaim"`
	ButtonLabel   string   `toml:"buttonLabel"`
}

type RetentionConfig struct {
	MetricsDays int `toml:"metricsDays"`
	HistoryDays int `toml:"historyDays"`
}

type NetworkConfig struct {
	Enabled         bool `toml:"enabled"`
	IntervalSeconds int  `toml:"intervalSeconds"`
	Workers         int  `toml:"workers"`
	TimeoutSeconds  int  `toml:"timeoutSeconds"`
	// PrivilegedPing uses raw ICMP sockets (needs root/CAP_NET_RAW).
	PrivilegedPing bool `toml:"privilegedPing"`
}

type SNMPConfig struct {
	Enabled bool `toml:"enabled"`
	Workers int  `toml:"workers"`
}

type MetricsConfig struct {
	// NCV extracts "name : value" lines from statuses sent by plugins
	// (xymond_rrd's NCV behaviour) and stores them as metrics.
	NCV bool `toml:"ncv"`
	// NCVExclude lists tests never NCV-parsed.
	NCVExclude []string `toml:"ncvExclude"`
}

func Default() *Config {
	return &Config{
		DataDir:  defaultDataDir(),
		LogLevel: "info",
		HTTP:     HTTPConfig{Listen: ":8080", SessionHours: 24 * 7},
		Database: DatabaseConfig{DSN: "postgres://gomon:gomon@localhost:5432/gomon?sslmode=disable"},
		Xymon: XymonConfig{
			Listen:                 ":1984",
			TLSListen:              ":1985",
			StatusSenders:          []string{"0.0.0.0/0", "::/0"},
			AdminSenders:           []string{"127.0.0.0/8", "::1/128"},
			AutoRegister:           true,
			MaxMessageSize:         10 * 1024 * 1024,
			DefaultLifetimeMinutes: 30,
		},
		OIDC:      OIDCConfig{Scopes: []string{"openid", "profile", "email"}, GroupsClaim: "groups", ButtonLabel: "Sign in with SSO"},
		Retention: RetentionConfig{MetricsDays: 90, HistoryDays: 365},
		Network:   NetworkConfig{Enabled: true, IntervalSeconds: 300, Workers: 32, TimeoutSeconds: 10},
		SNMP:      SNMPConfig{Enabled: true, Workers: 16},
		Metrics:   MetricsConfig{NCV: true, NCVExclude: []string{"info", "trends", "clientlog", "client"}},
	}
}

func defaultDataDir() string {
	if d := os.Getenv("GOMON_DATA_DIR"); d != "" {
		return d
	}
	if home, err := os.UserConfigDir(); err == nil {
		return filepath.Join(home, "gomon")
	}
	return "./data"
}

// Load reads the config file (creating one with defaults if it does not
// exist) and applies environment overrides.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = filepath.Join(cfg.DataDir, "config.toml")
	}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(sampleConfig), 0o640); err != nil {
			return nil, fmt.Errorf("write default config: %w", err)
		}
	case err != nil:
		return nil, err
	default:
		if err := toml.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Dir(path)
	}
	applyEnv(cfg)
	return cfg, nil
}

func applyEnv(c *Config) {
	str := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	boolean := func(key string, dst *bool) {
		if v, ok := os.LookupEnv(key); ok {
			b, err := strconv.ParseBool(v)
			if err == nil {
				*dst = b
			}
		}
	}
	list := func(key string, dst *[]string) {
		if v, ok := os.LookupEnv(key); ok {
			var out []string
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			*dst = out
		}
	}
	str("GOMON_DATA_DIR", &c.DataDir)
	str("GOMON_LOG_LEVEL", &c.LogLevel)
	str("GOMON_HTTP_LISTEN", &c.HTTP.Listen)
	str("GOMON_BASE_URL", &c.HTTP.BaseURL)
	str("GOMON_DATABASE_DSN", &c.Database.DSN)
	str("GOMON_XYMON_LISTEN", &c.Xymon.Listen)
	str("GOMON_XYMON_TLS_LISTEN", &c.Xymon.TLSListen)
	str("GOMON_XYMON_TLS_CERT", &c.Xymon.TLSCert)
	str("GOMON_XYMON_TLS_KEY", &c.Xymon.TLSKey)
	list("GOMON_XYMON_TLS_SERVER_NAMES", &c.Xymon.TLSServerNames)
	boolean("GOMON_XYMON_REQUIRE_CLIENT_CERT", &c.Xymon.RequireClientCert)
	list("GOMON_XYMON_STATUS_SENDERS", &c.Xymon.StatusSenders)
	list("GOMON_XYMON_ADMIN_SENDERS", &c.Xymon.AdminSenders)
	boolean("GOMON_OIDC_ENABLED", &c.OIDC.Enabled)
	str("GOMON_OIDC_ISSUER", &c.OIDC.Issuer)
	str("GOMON_OIDC_CLIENT_ID", &c.OIDC.ClientID)
	str("GOMON_OIDC_CLIENT_SECRET", &c.OIDC.ClientSecret)
	str("GOMON_OIDC_REDIRECT_URL", &c.OIDC.RedirectURL)
	boolean("GOMON_OIDC_DISABLE_BUILTIN_LOGIN", &c.OIDC.DisableBuiltinLogin)
	list("GOMON_OIDC_ALLOWED_GROUPS", &c.OIDC.AllowedGroups)
	boolean("GOMON_NETWORK_PRIVILEGED_PING", &c.Network.PrivilegedPing)
}

const sampleConfig = `# gomon configuration. Every value can also be set with a GOMON_* environment
# variable (see README). Values shown are the defaults.

# dataDir = "/var/lib/gomon"
logLevel = "info"

[http]
listen = ":8080"
# baseUrl = "https://gomon.example.com"
# secureCookies = false
sessionHours = 168

[database]
dsn = "postgres://gomon:gomon@localhost:5432/gomon?sslmode=disable"

[xymon]
# Plaintext Xymon protocol, for unmodified clients and ext scripts.
# Set to "127.0.0.1:1984" to only accept local plaintext (e.g. via gomon relay).
listen = ":1984"
# Xymon over TLS (upstream "xymons://" compatible). The certificate is
# issued automatically from gomon's built-in CA unless tlsCert/tlsKey are set.
tlsListen = ":1985"
# tlsCert = ""
# tlsKey = ""
# tlsServerNames = ["xymon.example.com"]
# clientCa = ""
requireClientCert = false
statusSenders = ["0.0.0.0/0", "::/0"]
adminSenders = ["127.0.0.0/8", "::1/128"]
autoRegister = true
defaultLifetimeMinutes = 30

[oidc]
enabled = false
# issuer = "https://auth.example.com/application/o/gomon/"
# clientId = ""
# clientSecret = ""
# redirectUrl = "https://gomon.example.com/api/auth/oidc/callback"
scopes = ["openid", "profile", "email"]
disableBuiltinLogin = false
# allowedGroups = ["monitoring"]
groupsClaim = "groups"

[retention]
metricsDays = 90
historyDays = 365

[network]
enabled = true
intervalSeconds = 300
workers = 32
timeoutSeconds = 10
privilegedPing = false

[snmp]
enabled = true
workers = 16

[metrics]
ncv = true
ncvExclude = ["info", "trends", "clientlog", "client"]
`
