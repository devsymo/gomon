// Package netmon is gomon's native replacement for xymonnet: it runs the
// network tests named in a host's hosts.cfg-style tags (conn, ssh, smtp,
// http://..., https://..., cont;URL;regex, httpstatus;URL;codes, ...).
package netmon

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"gomon/internal/engine"
	"gomon/internal/model"
	"gomon/internal/protocol"
)

// Services maps service tag names to TCP ports (Xymon protocols.cfg defaults).
var Services = map[string]int{
	"ftp": 21, "ssh": 22, "ssh1": 22, "ssh2": 22, "telnet": 23, "smtp": 25, "dns": 53, "pop": 110, "pop3": 110,
	"pop-3": 110, "nntp": 119, "imap": 143, "imap2": 143, "imap3": 220, "imap4": 143, "ldap": 389, "smtps": 465,
	"submission": 587, "rsync": 873, "ftps": 990, "imaps": 993, "pop3s": 995, "ldaps": 636, "telnets": 992,
	"nntps": 563, "mysql": 3306, "rdp": 3389, "pgsql": 5432, "postgres": 5432, "vnc": 5900, "redis": 6379,
	"amqp": 5672, "amqps": 5671, "mongodb": 27017, "memcached": 11211, "ircd": 6667, "oratns": 1521, "qmtp": 209,
	"qmqp": 628, "clamd": 3310, "spamd": 783, "svn": 3690, "git": 9418, "cupsd": 631, "jabber": 5222, "xmpp": 5222,
	"bbd": 1984, "xymon": 1984, "xymons": 1985, "elasticsearch": 9200, "kafka": 9092, "zookeeper": 2181,
}

// bannerServices send a greeting we can show in the status text.
var bannerServices = map[string]bool{"ftp": true, "ssh": true, "ssh1": true, "ssh2": true, "smtp": true, "pop": true,
	"pop3": true, "pop-3": true, "imap": true, "imap4": true, "nntp": true, "submission": true, "mysql": false}

// tlsServices are wrapped in TLS: their certificate feeds the sslcert column.
var tlsServices = map[string]bool{"smtps": true, "imaps": true, "pop3s": true, "ldaps": true, "ftps": true, "telnets": true, "nntps": true, "xymons": true}

type Config struct {
	Interval   time.Duration
	Workers    int
	Timeout    time.Duration
	Privileged bool
	SSLWarn    time.Duration
	SSLAlarm   time.Duration
}

type Monitor struct {
	eng *engine.Engine
	log *slog.Logger
	cfg Config
}

func New(eng *engine.Engine, log *slog.Logger, cfg Config) *Monitor {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 32
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.SSLWarn == 0 {
		cfg.SSLWarn = 30 * 24 * time.Hour
	}
	if cfg.SSLAlarm == 0 {
		cfg.SSLAlarm = 10 * 24 * time.Hour
	}
	return &Monitor{eng: eng, log: log, cfg: cfg}
}

func (m *Monitor) Run(ctx context.Context) {
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	select { // let the server settle before the first run
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
	}
	for {
		m.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce tests every host once.
func (m *Monitor) RunOnce(ctx context.Context) {
	hosts := m.eng.Hosts()
	jobs := make(chan *model.Host)
	var wg sync.WaitGroup
	for i := 0; i < m.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for h := range jobs {
				m.TestHost(ctx, h)
			}
		}()
	}
	for _, h := range hosts {
		if strings.TrimSpace(h.Tags) == "" {
			continue
		}
		select {
		case jobs <- h:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
}

type plan struct {
	conn     bool
	services []svcTest
	urls     []urlTest
}

type svcTest struct {
	name    string
	port    int
	reverse bool // "!svc": must NOT answer
	dialup  bool // "?svc": down is clear
}

type urlTest struct {
	url      string
	contains *regexp.Regexp
	codes    *regexp.Regexp
	invert   bool
}

// Plan parses a host's tags into tests.
func Plan(tags string) plan {
	var p plan
	noconn := false
	hasNet := false
	for _, t := range strings.Fields(tags) {
		lt := strings.ToLower(t)
		switch {
		case lt == "conn" || lt == "ping":
			p.conn = true
		case lt == "noconn" || lt == "noping":
			noconn = true
		case strings.HasPrefix(lt, "http://") || strings.HasPrefix(lt, "https://"):
			p.urls = append(p.urls, urlTest{url: t})
			hasNet = true
		case strings.HasPrefix(lt, "cont;") || strings.HasPrefix(lt, "cont=") || strings.HasPrefix(lt, "nocont;"):
			parts := strings.SplitN(t[strings.IndexAny(t, ";=")+1:], ";", 2)
			if len(parts) == 2 {
				if re, err := regexp.Compile(parts[1]); err == nil {
					p.urls = append(p.urls, urlTest{url: parts[0], contains: re, invert: strings.HasPrefix(lt, "nocont")})
					hasNet = true
				}
			}
		case strings.HasPrefix(lt, "httpstatus;") || strings.HasPrefix(lt, "httpstatus="):
			parts := strings.Split(t[11:], ";")
			if len(parts) >= 2 {
				if re, err := regexp.Compile("^(" + parts[1] + ")$"); err == nil {
					p.urls = append(p.urls, urlTest{url: parts[0], codes: re})
					hasNet = true
				}
			}
		default:
			name := lt
			reverse, dialup := false, false
			if strings.HasPrefix(name, "!") {
				reverse, name = true, name[1:]
			} else if strings.HasPrefix(name, "?") {
				dialup, name = true, name[1:]
			}
			port := 0
			if i := strings.IndexByte(name, ':'); i > 0 {
				port, _ = strconv.Atoi(name[i+1:])
				name = name[:i]
			}
			if def, ok := Services[name]; ok {
				if port == 0 {
					port = def
				}
				p.services = append(p.services, svcTest{name: name, port: port, reverse: reverse, dialup: dialup})
				hasNet = true
			}
		}
	}
	if hasNet && !noconn {
		p.conn = true
	}
	if noconn {
		p.conn = false
	}
	return p
}

func (m *Monitor) submit(ctx context.Context, host, test string, c protocol.Color, text string) {
	err := m.eng.Submit(ctx, engine.Update{Host: host, Test: test, Color: c, Text: text,
		Lifetime: 3 * m.cfg.Interval, Sender: "gomon-net", Transport: "local"})
	if err != nil {
		m.log.Warn("submit network test", "host", host, "test", test, "err", err)
	}
}

func (m *Monitor) TestHost(ctx context.Context, h *model.Host) {
	p := Plan(h.Tags)
	if !p.conn && len(p.services) == 0 && len(p.urls) == 0 {
		return
	}
	addr := h.IP
	if addr == "" || addr == "0.0.0.0" || net.ParseIP(addr) == nil {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, h.Name)
		if err != nil || len(ips) == 0 {
			text := fmt.Sprintf("%s - cannot resolve %s: %v\n", time.Now().Format(time.RFC1123), h.Name, err)
			if p.conn {
				m.submit(ctx, h.Name, "conn", protocol.Red, text)
			}
			return
		}
		addr = ips[0].IP.String()
	}
	now := time.Now().Format(time.RFC1123)

	if p.conn {
		rtt, loss, err := m.ping(ctx, addr)
		switch {
		case err != nil:
			m.submit(ctx, h.Name, "conn", protocol.Red, fmt.Sprintf("%s - %s is unreachable\n\n&red ping failed: %v\n", now, h.Name, err))
		case loss >= 100:
			m.submit(ctx, h.Name, "conn", protocol.Red, fmt.Sprintf("%s - %s is unreachable\n\n&red %s: 100%% packet loss\n", now, h.Name, addr))
		default:
			c := protocol.Green
			if loss > 0 {
				c = protocol.Yellow
			}
			m.submit(ctx, h.Name, "conn", c, fmt.Sprintf("%s - %s is alive\n\n&%s %s: %.0f%% packet loss, rtt %.3f ms\n", now, h.Name, c, addr, loss, float64(rtt.Microseconds())/1000))
			m.eng.RecordMetrics(h.Name, "conn", []protocol.Metric{{Name: "rtt_ms", Value: float64(rtt.Microseconds()) / 1000}}, time.Now())
		}
	}

	var certs []certInfo
	for _, s := range p.services {
		ok, banner, dur, cert, err := m.tcpCheck(ctx, addr, s)
		if cert != nil {
			certs = append(certs, *cert)
		}
		var c protocol.Color
		var text string
		switch {
		case s.reverse && ok:
			c, text = protocol.Red, fmt.Sprintf("%s - %s is running but should not be\n", now, s.name)
		case s.reverse:
			c, text = protocol.Green, fmt.Sprintf("%s - %s is not running (as expected)\n", now, s.name)
		case ok:
			c, text = protocol.Green, fmt.Sprintf("%s - %s ok\n\n&green Service %s on %s:%d is OK (%.3f s)\n", now, s.name, s.name, addr, s.port, dur.Seconds())
			if banner != "" {
				text += "\n" + banner + "\n"
			}
			m.eng.RecordMetrics(h.Name, s.name, []protocol.Metric{{Name: "seconds", Value: dur.Seconds()}}, time.Now())
		default:
			c = protocol.Red
			if s.dialup {
				c = protocol.Clear
			}
			text = fmt.Sprintf("%s - %s NOT ok\n\n&%s Service %s on %s:%d is not OK: %v\n", now, s.name, c, s.name, addr, s.port, err)
		}
		m.submit(ctx, h.Name, s.name, c, text)
	}

	if len(p.urls) > 0 {
		color := protocol.Green
		var sb strings.Builder
		for _, u := range p.urls {
			c, line, dur, cert := m.httpCheck(ctx, h, addr, u)
			if cert != nil {
				certs = append(certs, *cert)
			}
			color = protocol.Worse(color, c)
			sb.WriteString(line + "\n")
			if dur > 0 {
				m.eng.RecordMetrics(h.Name, "http", []protocol.Metric{{Name: metricName(u.url), Value: dur.Seconds()}}, time.Now())
			}
		}
		summary := "All URLs OK"
		if color != protocol.Green {
			summary = "URL check failed"
		}
		m.submit(ctx, h.Name, "http", color, fmt.Sprintf("%s - %s\n\n%s", now, summary, sb.String()))
	}

	if len(certs) > 0 {
		m.sslReport(ctx, h.Name, now, certs)
	}
}

func metricName(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return strings.TrimRight(u, "/")
}

func (m *Monitor) ping(ctx context.Context, addr string) (time.Duration, float64, error) {
	run := func(priv bool) (*probing.Statistics, error) {
		p, err := probing.NewPinger(addr)
		if err != nil {
			return nil, err
		}
		p.SetPrivileged(priv)
		p.Count = 3
		p.Interval = 300 * time.Millisecond
		p.Timeout = m.cfg.Timeout
		if err := p.RunWithContext(ctx); err != nil {
			return nil, err
		}
		return p.Statistics(), nil
	}
	st, err := run(m.cfg.Privileged)
	if err != nil {
		// Socket permission problems: try the other mode before giving up.
		st, err = run(!m.cfg.Privileged)
		if err != nil {
			return 0, 0, err
		}
	}
	return st.AvgRtt, st.PacketLoss, nil
}

type certInfo struct {
	where   string
	subject string
	expires time.Time
}

func (m *Monitor) tcpCheck(ctx context.Context, addr string, s svcTest) (bool, string, time.Duration, *certInfo, error) {
	start := time.Now()
	d := net.Dialer{Timeout: m.cfg.Timeout}
	target := net.JoinHostPort(addr, strconv.Itoa(s.port))
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return false, "", 0, nil, err
	}
	defer conn.Close()
	var cert *certInfo
	if tlsServices[s.name] {
		tc := tls.Client(conn, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // only reading the certificate
		_ = tc.SetDeadline(time.Now().Add(m.cfg.Timeout))
		if err := tc.HandshakeContext(ctx); err != nil {
			return false, "", 0, nil, fmt.Errorf("TLS handshake: %w", err)
		}
		if pc := tc.ConnectionState().PeerCertificates; len(pc) > 0 {
			cert = &certInfo{where: fmt.Sprintf("%s:%d", s.name, s.port), subject: pc[0].Subject.CommonName, expires: pc[0].NotAfter}
		}
		return true, "", time.Since(start), cert, nil
	}
	banner := ""
	if bannerServices[s.name] {
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, _ := bufio.NewReader(io.LimitReader(conn, 512)).ReadString('\n')
		banner = strings.TrimSpace(line)
	}
	return true, banner, time.Since(start), cert, nil
}

func (m *Monitor) httpCheck(ctx context.Context, h *model.Host, addr string, u urlTest) (protocol.Color, string, time.Duration, *certInfo) {
	var cert *certInfo
	tr := &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // cert validity reported via sslcert column
		ResponseHeaderTimeout: m.cfg.Timeout,
		DisableKeepAlives:     true,
	}
	// Xymon semantics: connect to the hosts.cfg address when the URL names this host.
	tr.DialContext = func(ctx context.Context, network, a string) (net.Conn, error) {
		hostPart, port, _ := net.SplitHostPort(a)
		if strings.EqualFold(hostPart, h.Name) && net.ParseIP(addr) != nil {
			a = net.JoinHostPort(addr, port)
		}
		return (&net.Dialer{Timeout: m.cfg.Timeout}).DialContext(ctx, network, a)
	}
	client := &http.Client{Transport: tr, Timeout: m.cfg.Timeout * 2}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.url, nil)
	if err != nil {
		return protocol.Red, fmt.Sprintf("&red %s - invalid URL: %v", u.url, err), 0, nil
	}
	req.Header.Set("User-Agent", "gomon/1.0 (Xymon-compatible)")
	start := time.Now()
	resp, err := client.Do(req)
	dur := time.Since(start)
	if err != nil {
		return protocol.Red, fmt.Sprintf("&red %s - %v", u.url, err), 0, nil
	}
	defer resp.Body.Close()
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		pc := resp.TLS.PeerCertificates[0]
		cert = &certInfo{where: u.url, subject: pc.Subject.CommonName, expires: pc.NotAfter}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	ok := resp.StatusCode < 400
	if u.codes != nil {
		ok = u.codes.MatchString(strconv.Itoa(resp.StatusCode))
	}
	detail := fmt.Sprintf("HTTP %d, %d bytes, %.3f s", resp.StatusCode, len(body), dur.Seconds())
	if ok && u.contains != nil {
		found := u.contains.Match(body)
		if found == u.invert {
			ok = false
			if u.invert {
				detail += ", forbidden content found"
			} else {
				detail += ", expected content not found"
			}
		}
	}
	if !ok {
		return protocol.Red, fmt.Sprintf("&red %s - %s", u.url, detail), dur, cert
	}
	return protocol.Green, fmt.Sprintf("&green %s - %s", u.url, detail), dur, cert
}

func (m *Monitor) sslReport(ctx context.Context, host, now string, certs []certInfo) {
	sort.Slice(certs, func(i, j int) bool { return certs[i].expires.Before(certs[j].expires) })
	color := protocol.Green
	var lines []string
	for _, c := range certs {
		left := time.Until(c.expires)
		cc := protocol.Green
		switch {
		case left < m.cfg.SSLAlarm:
			cc = protocol.Red
		case left < m.cfg.SSLWarn:
			cc = protocol.Yellow
		}
		color = protocol.Worse(color, cc)
		lines = append(lines, fmt.Sprintf("&%s %s: certificate for %s expires %s (%d days)", cc, c.where, c.subject,
			c.expires.Format("2006-01-02"), int(left.Hours()/24)))
		m.eng.RecordMetrics(host, "sslcert", []protocol.Metric{{Name: metricName(c.where), Value: left.Hours() / 24}}, time.Now())
	}
	summary := "SSL certificates OK"
	if color != protocol.Green {
		summary = "SSL certificate expiring"
	}
	m.submit(ctx, host, "sslcert", color, fmt.Sprintf("%s - %s\n\n%s\n", now, summary, strings.Join(lines, "\n")))
}
