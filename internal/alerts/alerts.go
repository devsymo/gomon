// Package alerts evaluates alert rules against live statuses and sends
// notifications (webhook, Slack, Discord, ntfy, email).
package alerts

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"regexp"
	"strings"
	"sync"
	"time"

	"gomon/internal/engine"
	"gomon/internal/model"
	"gomon/internal/protocol"
	"gomon/internal/store"
)

type Manager struct {
	eng     *engine.Engine
	store   *store.Store
	log     *slog.Logger
	baseURL string
	client  *http.Client

	mu    sync.Mutex
	state map[string]*alertState
	kick  chan struct{}
}

type alertState struct {
	notified  bool
	lastSent  time.Time
	sentColor protocol.Color
}

func New(eng *engine.Engine, st *store.Store, log *slog.Logger, baseURL string) *Manager {
	return &Manager{eng: eng, store: st, log: log, baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{Timeout: 15 * time.Second}, state: map[string]*alertState{}, kick: make(chan struct{}, 1)}
}

func (m *Manager) Run(ctx context.Context) {
	events, cancel := m.eng.Subscribe()
	defer cancel()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == engine.EventStatus && ev.Changed {
				m.evaluate(ctx)
			}
		case <-m.kick:
			m.evaluate(ctx)
		case <-t.C:
			m.evaluate(ctx)
		}
	}
}

// Kick triggers an evaluation (after rules change).
func (m *Manager) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

type compiledRule struct {
	*model.AlertRule
	host, test, exHost, exTest *regexp.Regexp
	colors                     map[protocol.Color]bool
}

func compile(p string) *regexp.Regexp {
	if strings.TrimSpace(p) == "" {
		return nil
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return regexp.MustCompile(`^` + regexp.QuoteMeta(p) + `$`)
	}
	return re
}

func alertColors(min string) map[protocol.Color]bool {
	switch min {
	case "yellow":
		return map[protocol.Color]bool{protocol.Yellow: true, protocol.Red: true, protocol.Purple: true}
	case "purple":
		return map[protocol.Color]bool{protocol.Purple: true}
	default:
		return map[protocol.Color]bool{protocol.Red: true, protocol.Purple: true}
	}
}

func (r *compiledRule) matches(st *model.Status) bool {
	if r.host != nil && !r.host.MatchString(st.Host) {
		return false
	}
	if r.test != nil && !r.test.MatchString(st.Test) {
		return false
	}
	if r.exHost != nil && r.exHost.MatchString(st.Host) {
		return false
	}
	if r.exTest != nil && r.exTest.MatchString(st.Test) {
		return false
	}
	return true
}

func (m *Manager) evaluate(ctx context.Context) {
	rules, err := m.store.ListAlertRules(ctx)
	if err != nil {
		return
	}
	chans, err := m.store.ListAlertChannels(ctx)
	if err != nil {
		return
	}
	chByID := map[int64]*model.AlertChannel{}
	for _, c := range chans {
		chByID[c.ID] = c
	}
	var crs []*compiledRule
	for _, r := range rules {
		if !r.Enabled || len(r.ChannelIDs) == 0 {
			continue
		}
		crs = append(crs, &compiledRule{AlertRule: r, host: compile(r.HostPattern), test: compile(r.TestPattern),
			exHost: compile(r.ExcludeHosts), exTest: compile(r.ExcludeTests), colors: alertColors(r.MinColor)})
	}
	now := time.Now()
	statuses := m.eng.Statuses(nil, false)
	seen := map[string]bool{}

	type send struct {
		rule     *compiledRule
		st       *model.Status
		recovery bool
	}
	var sends []send

	m.mu.Lock()
	for _, st := range statuses {
		for _, r := range crs {
			if !r.matches(st) {
				continue
			}
			k := fmt.Sprintf("%d|%s|%s", r.ID, st.Host, st.Test)
			bad := r.colors[st.Color] && !st.Acked(now) && !st.Disabled(now)
			as := m.state[k]
			if bad {
				seen[k] = true
				if as == nil {
					as = &alertState{}
					m.state[k] = as
				}
				if now.Sub(st.LastChange) < time.Duration(r.DelayMinutes)*time.Minute {
					continue
				}
				repeat := r.RepeatMinutes > 0 && now.Sub(as.lastSent) >= time.Duration(r.RepeatMinutes)*time.Minute
				if !as.notified || repeat || st.Color > as.sentColor {
					as.notified, as.lastSent, as.sentColor = true, now, st.Color
					sends = append(sends, send{r, st, false})
				}
			} else if as != nil {
				if as.notified && r.NotifyRecovery && (st.Color == protocol.Green || st.Color == protocol.Clear) {
					sends = append(sends, send{r, st, true})
				}
				if !(st.Acked(now) || st.Disabled(now)) || !as.notified {
					delete(m.state, k)
				}
				seen[k] = true
			}
		}
	}
	for k := range m.state {
		if !seen[k] {
			delete(m.state, k) // status dropped
		}
	}
	m.mu.Unlock()

	for _, s := range sends {
		for _, id := range s.rule.ChannelIDs {
			ch := chByID[id]
			if ch == nil || !ch.Enabled {
				continue
			}
			n := m.notification(s.st, s.recovery)
			go func(ch *model.AlertChannel) {
				sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := m.Send(sctx, ch, n); err != nil {
					m.log.Warn("alert delivery failed", "channel", ch.Name, "host", n.Host, "test", n.Test, "err", err)
				} else {
					m.log.Info("alert sent", "channel", ch.Name, "host", n.Host, "test", n.Test, "color", n.Color)
				}
			}(ch)
		}
	}
}

type Notification struct {
	Event   string    `json:"event"` // "alert" or "recovery" or "test"
	Host    string    `json:"host"`
	Test    string    `json:"test"`
	Color   string    `json:"color"`
	Summary string    `json:"summary"`
	Message string    `json:"message"`
	Since   time.Time `json:"since"`
	URL     string    `json:"url"`
	Cookie  int64     `json:"cookie,omitempty"`
}

func (m *Manager) notification(st *model.Status, recovery bool) Notification {
	full, _ := m.eng.Status(st.Host, st.Test)
	msg := ""
	if full != nil {
		msg = full.Message
	}
	ev := "alert"
	if recovery {
		ev = "recovery"
	}
	return Notification{Event: ev, Host: st.Host, Test: st.Test, Color: st.Color.String(), Summary: st.Summary,
		Message: msg, Since: st.LastChange, URL: m.baseURL + "/hosts/" + st.Host + "/" + st.Test, Cookie: st.Cookie}
}

func (n Notification) Title() string {
	if n.Event == "recovery" {
		return fmt.Sprintf("RECOVERED %s.%s", n.Host, n.Test)
	}
	return fmt.Sprintf("%s %s.%s", strings.ToUpper(n.Color), n.Host, n.Test)
}

func (n Notification) Body() string {
	var sb strings.Builder
	sb.WriteString(n.Summary)
	if n.URL != "" && !strings.HasPrefix(n.URL, "/") {
		sb.WriteString("\n" + n.URL)
	}
	return sb.String()
}

// TestNotification is sent by the "Send test" button.
func TestNotification(baseURL string) Notification {
	return Notification{Event: "test", Host: "gomon", Test: "test", Color: "yellow", Summary: "This is a test notification from gomon",
		Since: time.Now(), URL: strings.TrimRight(baseURL, "/")}
}

func (m *Manager) postJSON(ctx context.Context, url string, body any, headers map[string]string) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// Send delivers one notification through a channel.
func (m *Manager) Send(ctx context.Context, ch *model.AlertChannel, n Notification) error {
	c := ch.Config
	switch ch.Type {
	case "webhook":
		h := map[string]string{}
		if a := c["authorization"]; a != "" {
			h["Authorization"] = a
		}
		return m.postJSON(ctx, c["url"], n, h)
	case "slack":
		emoji := map[string]string{"red": ":red_circle:", "yellow": ":large_yellow_circle:", "purple": ":purple_circle:", "green": ":large_green_circle:"}[n.Color]
		if n.Event == "recovery" {
			emoji = ":large_green_circle:"
		}
		return m.postJSON(ctx, c["url"], map[string]string{"text": fmt.Sprintf("%s *%s*\n%s", emoji, n.Title(), n.Body())}, nil)
	case "discord":
		return m.postJSON(ctx, c["url"], map[string]string{"content": fmt.Sprintf("**%s**\n%s", n.Title(), n.Body())}, nil)
	case "ntfy":
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c["url"], strings.NewReader(n.Body()))
		if err != nil {
			return err
		}
		req.Header.Set("Title", n.Title())
		prio := "default"
		if n.Color == "red" && n.Event == "alert" {
			prio = "high"
		}
		req.Header.Set("Priority", prio)
		if t := c["token"]; t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
		resp, err := m.client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return nil
	case "email":
		return sendMail(c, n)
	}
	return fmt.Errorf("unknown channel type %q", ch.Type)
}

func sendMail(c map[string]string, n Notification) error {
	host, port := c["host"], c["port"]
	if host == "" {
		return errors.New("smtp host not set")
	}
	if port == "" {
		port = "587"
	}
	from := c["from"]
	var to []string
	for _, t := range strings.Split(c["to"], ",") {
		if t = strings.TrimSpace(t); t != "" {
			to = append(to, t)
		}
	}
	if from == "" || len(to) == 0 {
		return errors.New("from and to are required")
	}
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: [gomon] %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n\r\n%s\r\n",
		from, strings.Join(to, ", "), n.Title(), n.Body(), n.Message)
	addr := net.JoinHostPort(host, port)
	var auth smtp.Auth
	if c["username"] != "" {
		auth = smtp.PlainAuth("", c["username"], c["password"], host)
	}
	if c["tls"] == "tls" || port == "465" {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		cl, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer cl.Close()
		if auth != nil {
			if err := cl.Auth(auth); err != nil {
				return err
			}
		}
		if err := cl.Mail(from); err != nil {
			return err
		}
		for _, t := range to {
			if err := cl.Rcpt(t); err != nil {
				return err
			}
		}
		w, err := cl.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return cl.Quit()
	}
	// net/smtp uses STARTTLS automatically when offered.
	return smtp.SendMail(addr, auth, from, to, []byte(body))
}
