// Command gomon is a Xymon-compatible monitoring server with an embedded
// web UI, plus client-side helpers (a drop-in `xymon` sender and a TLS relay).
package main

import (
	"bufio"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gomon/internal/alerts"
	"gomon/internal/api"
	"gomon/internal/auth"
	"gomon/internal/cfgfiles"
	"gomon/internal/config"
	"gomon/internal/engine"
	"gomon/internal/hostscfg"
	"gomon/internal/model"
	"gomon/internal/netmon"
	"gomon/internal/pki"
	"gomon/internal/relay"
	"gomon/internal/snmp"
	"gomon/internal/store"
	"gomon/internal/xclient"
	"gomon/internal/xymond"
	"gomon/web"
)

var version = "dev"

func main() {
	// Invoked as "xymon" (symlink): behave exactly like xymon(1).
	if base := filepath.Base(os.Args[0]); base == "xymon" || base == "bb" {
		os.Exit(xymonCompat(os.Args[1:]))
	}
	var cfgPath string
	root := &cobra.Command{Use: "gomon", Short: "Xymon-compatible monitoring server", SilenceUsage: true}
	root.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "config file (default $GOMON_DATA_DIR/config.toml)")

	root.AddCommand(&cobra.Command{Use: "serve", Short: "Run the gomon server", RunE: func(cmd *cobra.Command, args []string) error {
		return serve(cfgPath)
	}})
	root.AddCommand(&cobra.Command{Use: "version", Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("gomon %s (xymon protocol %s)\n", version, xymond.Version)
	}})
	root.AddCommand(&cobra.Command{
		Use:                "send [xymon options] RECIPIENT MESSAGE",
		Short:              "Send a Xymon message (drop-in for the xymon command; symlink gomon to 'xymon')",
		DisableFlagParsing: true,
		Run:                func(cmd *cobra.Command, args []string) { os.Exit(xymonCompat(args)) },
	})
	root.AddCommand(relayCmd())
	root.AddCommand(certCmd(&cfgPath))
	root.AddCommand(userCmd(&cfgPath))
	root.AddCommand(importCmd(&cfgPath))
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func serve(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("starting gomon", "version", version, "dataDir", cfg.DataDir)
	st, err := store.Open(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	eng := engine.New(st, log, engine.Options{
		DefaultLifetime: time.Duration(cfg.Xymon.DefaultLifetimeMinutes) * time.Minute,
		AutoRegister:    cfg.Xymon.AutoRegister,
		NCV:             cfg.Metrics.NCV,
		NCVExclude:      cfg.Metrics.NCVExclude,
	})
	if err := eng.Load(ctx); err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	cfgs := cfgfiles.New(st)
	if err := cfgs.Load(ctx); err != nil {
		return err
	}
	ca, err := pki.LoadOrCreate(filepath.Join(cfg.DataDir, "pki"))
	if err != nil {
		return fmt.Errorf("certificate authority: %w", err)
	}

	xs := xymond.New(eng, st, cfgs, log, xymond.Options{
		StatusSenders: cfg.Xymon.StatusSenders, AdminSenders: cfg.Xymon.AdminSenders,
		MaxMessageSize: cfg.Xymon.MaxMessageSize, RequireClientCert: cfg.Xymon.RequireClientCert,
	})
	nm := netmon.New(eng, log, netmon.Config{
		Interval: time.Duration(cfg.Network.IntervalSeconds) * time.Second, Workers: cfg.Network.Workers,
		Timeout: time.Duration(cfg.Network.TimeoutSeconds) * time.Second, Privileged: cfg.Network.PrivilegedPing,
	})
	sp := snmp.New(eng, st, log, cfg.SNMP.Workers)
	am := alerts.New(eng, st, log, cfg.HTTP.BaseURL)
	au := auth.New(st, cfg, log)

	a := api.New(api.Deps{Cfg: cfg, Store: st, Engine: eng, Auth: au, Cfgs: cfgs, Alerts: am, Net: nm, SNMP: sp,
		CA: ca, Xymond: xs, Web: web.FS(), Version: version, Log: log, Started: time.Now()})

	var wg sync.WaitGroup
	errc := make(chan error, 8)
	run := func(name string, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}
	run("engine", func() error { eng.Run(ctx); return nil })
	run("alerts", func() error { am.Run(ctx); return nil })
	if cfg.Network.Enabled {
		run("netmon", func() error { nm.Run(ctx); return nil })
	}
	if cfg.SNMP.Enabled {
		run("snmp", func() error { sp.Run(ctx); return nil })
	}
	run("retention", func() error { retention(ctx, st, cfg, log); return nil })

	if cfg.Xymon.Listen != "" {
		run("xymon", func() error { return xs.ListenPlain(ctx, cfg.Xymon.Listen) })
	}
	if cfg.Xymon.TLSListen != "" {
		certPath, keyPath := cfg.Xymon.TLSCert, cfg.Xymon.TLSKey
		if certPath == "" {
			certPath, keyPath, err = ca.IssueServer(pki.DefaultServerNames(cfg.Xymon.TLSServerNames))
			if err != nil {
				return fmt.Errorf("issue server certificate: %w", err)
			}
		}
		rl, err := pki.NewCertReloader(certPath, keyPath)
		if err != nil {
			return fmt.Errorf("load TLS certificate: %w", err)
		}
		pool := ca.Pool()
		if cfg.Xymon.ClientCA != "" {
			pem, err := os.ReadFile(cfg.Xymon.ClientCA)
			if err != nil {
				return err
			}
			pool = x509.NewCertPool()
			pool.AppendCertsFromPEM(pem)
		}
		tcfg := xymond.TLSConfig(rl.GetCertificate, pool, cfg.Xymon.RequireClientCert)
		run("xymon-tls", func() error { return xs.ListenTLS(ctx, cfg.Xymon.TLSListen, tcfg) })
	}

	hs := &http.Server{Addr: cfg.HTTP.Listen, Handler: a.Router(), ReadHeaderTimeout: 15 * time.Second}
	run("http", func() error {
		log.Info("web UI listening", "addr", cfg.HTTP.Listen)
		return hs.ListenAndServe()
	})

	select {
	case <-ctx.Done():
	case err := <-errc:
		log.Error("fatal", "err", err)
		stop()
	}
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = hs.Shutdown(shCtx)
	wg.Wait()
	log.Info("stopped")
	return nil
}

func retention(ctx context.Context, st *store.Store, cfg *config.Config, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		now := time.Now()
		if err := st.Prune(ctx, now.AddDate(0, 0, -cfg.Retention.MetricsDays), now.AddDate(0, 0, -cfg.Retention.HistoryDays)); err != nil && ctx.Err() == nil {
			log.Warn("retention", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// xymonCompat implements the xymon(1) command line:
//
//	xymon [--debug] [--merge] [--response] [--timeout=N] RECIPIENT MESSAGE
//
// MESSAGE "@" (or "-") reads the message from stdin. RECIPIENT may be
// host, host:port, xymon://host or xymons://host (TLS).
func xymonCompat(args []string) int {
	o := xclient.FromEnv()
	var pos []string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--timeout="):
			if d, err := time.ParseDuration(strings.TrimPrefix(a, "--timeout=") + "s"); err == nil {
				o.Timeout = d
			}
		case strings.HasPrefix(a, "--"):
			// --debug, --merge, --response, --proxy=...: accepted
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: xymon [--timeout=N] RECIPIENT MESSAGE   (MESSAGE \"@\" reads stdin)")
		return 1
	}
	msg := pos[1]
	if msg == "@" || msg == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		msg = string(b)
	}
	recipients := []string{pos[0]}
	if pos[0] == "0.0.0.0" || pos[0] == "0" {
		recipients = strings.Fields(os.Getenv("XYMSERVERS"))
	}
	code := 0
	printed := false
	for _, rs := range recipients {
		r, err := xclient.ParseRecipient(rs, o)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
			continue
		}
		resp, err := xclient.Send(context.Background(), r, msg, o)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Whoops ! Failed to send message (%v)\n", err)
			code = 1
			continue
		}
		if !printed && resp != "" {
			fmt.Print(resp)
			printed = true
		}
	}
	if len(recipients) == 0 {
		fmt.Fprintln(os.Stderr, "No recipient to send to!")
		return 1
	}
	return code
}

func relayCmd() *cobra.Command {
	var listen string
	var servers []string
	var ca, cert, key, verify, sni string
	cmd := &cobra.Command{
		Use:   "relay",
		Short: "Accept plaintext Xymon messages locally and forward them encrypted (TLS) to gomon",
		Long: `Runs on a monitored host. Point existing agents and ext scripts at the relay
(XYMSRV=127.0.0.1) and they are forwarded over TLS without any changes:

  gomon relay --listen 127.0.0.1:1984 --server xymons://gomon.example.com \
      --ca /etc/gomon/ca.pem --cert /etc/gomon/host.pem --key /etc/gomon/host.key`,
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger("info")
			o := xclient.FromEnv()
			if ca != "" {
				o.TLS.CA = ca
			}
			if cert != "" {
				o.TLS.Cert = cert
			}
			if key != "" {
				o.TLS.Key = key
			}
			if verify != "" {
				o.TLS.Verify = verify
			}
			if sni != "" {
				o.TLS.SNI = sni
			}
			var ups []xclient.Recipient
			for _, s := range servers {
				r, err := xclient.ParseRecipient(s, o)
				if err != nil {
					return err
				}
				if !r.TLS {
					log.Warn("upstream is not TLS; messages will be forwarded in plaintext", "server", s)
				}
				ups = append(ups, r)
			}
			if len(ups) == 0 {
				return errors.New("at least one --server is required")
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return (&relay.Relay{Listen: listen, Upstreams: ups, Opts: o, Log: log}).Run(ctx)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:1984", "plaintext listen address")
	cmd.Flags().StringSliceVar(&servers, "server", nil, "upstream, e.g. xymons://gomon.example.com (repeatable)")
	cmd.Flags().StringVar(&ca, "ca", "", "CA bundle to verify the server (XYMON_TLS_CA)")
	cmd.Flags().StringVar(&cert, "cert", "", "client certificate naming this host (XYMON_TLS_CERT)")
	cmd.Flags().StringVar(&key, "key", "", "client key (XYMON_TLS_KEY)")
	cmd.Flags().StringVar(&verify, "verify", "", "full (default), peer or none (XYMON_TLS_VERIFY)")
	cmd.Flags().StringVar(&sni, "sni", "", "server name to verify (XYMON_TLS_SNI)")
	return cmd
}

func loadCA(cfgPath string) (*pki.CA, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	return pki.LoadOrCreate(filepath.Join(cfg.DataDir, "pki"))
}

func certCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{Use: "cert", Short: "Manage agent TLS certificates (built-in CA)"}
	cmd.AddCommand(&cobra.Command{Use: "ca", Short: "Print the CA certificate", RunE: func(c *cobra.Command, args []string) error {
		ca, err := loadCA(*cfgPath)
		if err != nil {
			return err
		}
		fmt.Print(string(ca.PEM))
		return nil
	}})
	var out string
	var days int
	issue := &cobra.Command{Use: "issue HOSTNAME", Short: "Issue a client certificate for HOSTNAME", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ca, err := loadCA(*cfgPath)
			if err != nil {
				return err
			}
			cert, key, err := ca.IssueClient(args[0], days)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(out, 0o700); err != nil {
				return err
			}
			base := filepath.Join(out, strings.ToLower(args[0]))
			if err := os.WriteFile(base+".key", key, 0o600); err != nil {
				return err
			}
			if err := os.WriteFile(base+".pem", cert, 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, "ca.pem"), ca.PEM, 0o644); err != nil {
				return err
			}
			fmt.Printf("wrote %s.pem, %s.key and %s\n", base, base, filepath.Join(out, "ca.pem"))
			return nil
		}}
	issue.Flags().StringVarP(&out, "out", "o", ".", "output directory")
	issue.Flags().IntVar(&days, "days", 365, "validity in days")
	cmd.AddCommand(issue)
	var host string
	sign := &cobra.Command{Use: "sign CSR_FILE", Short: "Sign a CSR made on the client host", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ca, err := loadCA(*cfgPath)
			if err != nil {
				return err
			}
			csr, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			cert, err := ca.SignCSR(csr, host, days)
			if err != nil {
				return err
			}
			fmt.Print(string(cert))
			return nil
		}}
	sign.Flags().StringVar(&host, "host", "", "hostname to certify (default: from the CSR)")
	sign.Flags().IntVar(&days, "days", 365, "validity in days")
	cmd.AddCommand(sign)
	return cmd
}

func openStore(cfgPath string) (*store.Store, *config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(context.Background(), cfg.Database.DSN)
	return st, cfg, err
}

func readPassword() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func userCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage built-in user accounts"}
	for _, action := range []string{"create", "passwd"} {
		action := action
		cmd.AddCommand(&cobra.Command{Use: action + " USERNAME", Args: cobra.ExactArgs(1),
			Short: map[string]string{"create": "Create a user", "passwd": "Change a user's password"}[action],
			RunE: func(c *cobra.Command, args []string) error {
				st, _, err := openStore(*cfgPath)
				if err != nil {
					return err
				}
				defer st.Close()
				pw, err := readPassword()
				if err != nil {
					return err
				}
				hash, err := auth.HashPassword(pw)
				if err != nil {
					return err
				}
				if action == "create" {
					return st.CreateUser(context.Background(), args[0], hash)
				}
				return st.SetPassword(context.Background(), args[0], hash)
			}})
	}
	return cmd
}

func importCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{Use: "import-hosts HOSTS_CFG", Short: "Import hosts from a Xymon hosts.cfg", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			st, _, err := openStore(*cfgPath)
			if err != nil {
				return err
			}
			defer st.Close()
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			parsed := hostscfg.Parse(string(b))
			entries := parsed.Entries
			for _, w := range parsed.Warnings {
				fmt.Fprintln(os.Stderr, "warning:", w)
			}
			ctx := context.Background()
			if err := st.SetPageTitles(ctx, parsed.Titles); err != nil {
				return err
			}
			for _, e := range entries {
				h, err := st.GetHost(ctx, e.Name)
				if err != nil {
					h = &model.Host{Name: e.Name}
				}
				h.IP, h.Tags, h.Page, h.Group = e.IP, e.Tags, e.Page, e.Group
				if err := st.UpsertHost(ctx, h); err != nil {
					return err
				}
			}
			fmt.Printf("imported %d hosts (restart gomon or use the web UI import to apply immediately)\n", len(entries))
			return nil
		}}
}
