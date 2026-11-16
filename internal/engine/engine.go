// Package engine holds gomon's live monitoring state: current statuses,
// color transitions, purple (stale) detection, acks, disables and the
// event stream used by alerting and the web UI.
package engine

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gomon/internal/model"
	"gomon/internal/protocol"
	"gomon/internal/store"
)

var ErrUnknownHost = errors.New("unknown host")

// untilOK is the DisabledUntil sentinel for "disabled until the status is OK".
var untilOK = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

type EventType string

const (
	EventStatus  EventType = "status"  // status updated (Changed tells whether the color changed)
	EventDropped EventType = "dropped" // status or host removed
	EventHost    EventType = "host"    // host added/changed/removed
)

type Event struct {
	Type      EventType      `json:"type"`
	Host      string         `json:"host"`
	Test      string         `json:"test,omitempty"`
	Color     protocol.Color `json:"-"`
	PrevColor protocol.Color `json:"-"`
	Changed   bool           `json:"changed"`
	Status    *model.Status  `json:"-"`
	Time      time.Time      `json:"time"`
}

type Options struct {
	DefaultLifetime time.Duration
	AutoRegister    bool
	NCV             bool
	NCVExclude      []string
}

type Engine struct {
	store *store.Store
	log   *slog.Logger
	opts  Options

	mu       sync.RWMutex
	statuses map[string]*model.Status // key host|test
	hosts    map[string]*model.Host
	cookie   int64

	metrics chan model.MetricSample

	subMu  sync.Mutex
	subs   map[int]chan Event
	nextID int
}

func New(st *store.Store, log *slog.Logger, opts Options) *Engine {
	if opts.DefaultLifetime <= 0 {
		opts.DefaultLifetime = 30 * time.Minute
	}
	return &Engine{
		store:    st,
		log:      log,
		opts:     opts,
		statuses: map[string]*model.Status{},
		hosts:    map[string]*model.Host{},
		metrics:  make(chan model.MetricSample, 200_000),
		subs:     map[int]chan Event{},
		cookie:   time.Now().Unix() % 1_000_000,
	}
}

func key(host, test string) string { return host + "|" + test }

// Load reads the persisted state.
func (e *Engine) Load(ctx context.Context) error {
	hosts, err := e.store.ListHosts(ctx)
	if err != nil {
		return err
	}
	sts, err := e.store.LoadStatuses(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range hosts {
		e.hosts[h.Name] = h
	}
	for _, s := range sts {
		e.statuses[key(s.Host, s.Test)] = s
		if s.Cookie > e.cookie {
			e.cookie = s.Cookie
		}
	}
	return nil
}

// Run starts the background loops and blocks until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e.metricsLoop(ctx) }()
	go func() { defer wg.Done(); e.sweepLoop(ctx) }()
	wg.Wait()
}

// ---- subscriptions ----

// Subscribe returns a channel receiving every event. Slow subscribers drop events.
func (e *Engine) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 1024)
	e.subMu.Lock()
	id := e.nextID
	e.nextID++
	e.subs[id] = ch
	e.subMu.Unlock()
	return ch, func() {
		e.subMu.Lock()
		if c, ok := e.subs[id]; ok {
			delete(e.subs, id)
			close(c)
		}
		e.subMu.Unlock()
	}
}

func (e *Engine) publish(ev Event) {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	for _, ch := range e.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// ---- hosts ----

func (e *Engine) Host(name string) (*model.Host, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	h, ok := e.hosts[name]
	if !ok {
		return nil, false
	}
	c := *h
	return &c, true
}

func (e *Engine) Hosts() []*model.Host {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*model.Host, 0, len(e.hosts))
	for _, h := range e.hosts {
		c := *h
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ResolveHost maps a reported name to a known host, registering it when
// auto-registration is enabled.
func (e *Engine) ResolveHost(ctx context.Context, name, sender string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", ErrUnknownHost
	}
	e.mu.RLock()
	_, ok := e.hosts[name]
	e.mu.RUnlock()
	if ok {
		return name, nil
	}
	if !e.opts.AutoRegister {
		return "", ErrUnknownHost
	}
	h := &model.Host{Name: name, IP: sender, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := e.store.EnsureHost(ctx, name, sender); err != nil {
		return "", err
	}
	e.mu.Lock()
	if _, ok := e.hosts[name]; !ok {
		e.hosts[name] = h
	}
	e.mu.Unlock()
	e.log.Info("registered new host", "host", name, "sender", sender)
	e.publish(Event{Type: EventHost, Host: name, Time: time.Now()})
	return name, nil
}

func (e *Engine) SaveHost(ctx context.Context, h *model.Host) error {
	h.Name = strings.ToLower(strings.TrimSpace(h.Name))
	if h.Name == "" {
		return errors.New("host name required")
	}
	if err := e.store.UpsertHost(ctx, h); err != nil {
		return err
	}
	fresh, err := e.store.GetHost(ctx, h.Name)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.hosts[h.Name] = fresh
	e.mu.Unlock()
	e.publish(Event{Type: EventHost, Host: h.Name, Time: time.Now()})
	return nil
}

func (e *Engine) SetHostOS(ctx context.Context, host, os, class string) {
	e.mu.Lock()
	h, ok := e.hosts[host]
	changed := ok && (h.OS != os || (class != "" && h.Class != class))
	if changed {
		h.OS = os
		if class != "" {
			h.Class = class
		}
	}
	e.mu.Unlock()
	if changed {
		if err := e.store.SetHostOS(ctx, host, os, class); err != nil {
			e.log.Warn("set host os", "err", err)
		}
	}
}

func (e *Engine) DeleteHost(ctx context.Context, name string) error {
	if err := e.store.DeleteHost(ctx, name); err != nil {
		return err
	}
	e.mu.Lock()
	delete(e.hosts, name)
	for k, s := range e.statuses {
		if s.Host == name {
			delete(e.statuses, k)
		}
	}
	e.mu.Unlock()
	e.publish(Event{Type: EventDropped, Host: name, Time: time.Now()})
	return nil
}

func (e *Engine) RenameHost(ctx context.Context, from, to string) error {
	from, to = strings.ToLower(from), strings.ToLower(to)
	if err := e.store.RenameHost(ctx, from, to); err != nil {
		return err
	}
	e.mu.Lock()
	if h, ok := e.hosts[from]; ok {
		delete(e.hosts, from)
		h.Name = to
		e.hosts[to] = h
	}
	for k, s := range e.statuses {
		if s.Host == from {
			delete(e.statuses, k)
			s.Host = to
			e.statuses[key(to, s.Test)] = s
		}
	}
	e.mu.Unlock()
	e.publish(Event{Type: EventHost, Host: to, Time: time.Now()})
	return nil
}

// ---- statuses ----

// Update is a status report from any source.
type Update struct {
	Host      string
	Test      string
	Color     protocol.Color
	Text      string
	Lifetime  time.Duration
	Group     string
	Sender    string
	Transport string
	Time      time.Time
}

// Submit applies a status update. The host must already be resolved.
func (e *Engine) Submit(ctx context.Context, u Update) error {
	if u.Time.IsZero() {
		u.Time = time.Now()
	}
	if u.Lifetime <= 0 {
		u.Lifetime = e.opts.DefaultLifetime
	}
	if u.Test == "" {
		return errors.New("empty test name")
	}
	now := u.Time
	summary := strings.TrimSpace(protocol.FirstLine(u.Text))

	e.mu.Lock()
	k := key(u.Host, u.Test)
	st, exists := e.statuses[k]
	if !exists {
		st = &model.Status{Host: u.Host, Test: u.Test, LastChange: now, Color: -1}
		e.statuses[k] = st
	}
	prev := st.Color
	st.RealColor = u.Color
	st.Summary = summary
	st.Message = u.Text
	st.LastUpdate = now
	st.ValidUntil = now.Add(u.Lifetime)
	st.Sender = u.Sender
	st.Transport = u.Transport
	st.Group = u.Group

	if st.DisabledUntil != nil {
		if st.DisabledUntil.Equal(untilOK) && u.Color == protocol.Green {
			st.DisabledUntil, st.DisableMsg = nil, ""
		} else if !st.DisabledUntil.After(now) {
			st.DisabledUntil, st.DisableMsg = nil, ""
		}
	}
	eff := u.Color
	if st.Disabled(now) {
		eff = protocol.Blue
	}
	st.Color = eff
	if !eff.IsAlert() {
		st.AckUntil, st.AckMsg, st.AckBy = nil, "", ""
	}
	changed := prev != eff
	if changed {
		st.LastChange = now
		if eff.IsAlert() && !prev.IsAlert() {
			e.cookie++
			st.Cookie = e.cookie
		}
	}
	snap := *st
	e.mu.Unlock()

	if err := e.store.SaveStatus(ctx, &snap); err != nil {
		return err
	}
	if changed {
		pc := prev
		if !exists {
			pc = protocol.Clear
		}
		ev := &model.HistoryEvent{Host: snap.Host, Test: snap.Test, Time: now, Color: eff, PrevColor: pc, Summary: summary, Message: u.Text}
		if err := e.store.AddHistory(ctx, ev); err != nil {
			e.log.Warn("history insert", "err", err)
		}
	}
	e.publish(Event{Type: EventStatus, Host: snap.Host, Test: snap.Test, Color: eff, PrevColor: prev, Changed: changed, Status: &snap, Time: now})

	if e.opts.NCV && !builtinTest(u.Test) && !contains(e.opts.NCVExclude, u.Test) {
		if ms := protocol.ParseNCV(u.Text, 50); len(ms) > 0 {
			e.RecordMetrics(u.Host, u.Test, ms, now)
		}
	}
	return nil
}

// builtinTest: columns whose metrics gomon extracts itself.
func builtinTest(t string) bool {
	switch t {
	case "cpu", "disk", "inode", "memory", "procs", "ports", "msgs", "conn", "http", "snmp", "if", "files", "info", "trends", "client", "uptime":
		return true
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Status returns a copy of one status.
func (e *Engine) Status(host, test string) (*model.Status, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st, ok := e.statuses[key(host, test)]
	if !ok {
		return nil, false
	}
	c := *st
	return &c, true
}

// Statuses returns copies of all statuses matching the filter (nil = all).
// Messages are included only when withMessage is set.
func (e *Engine) Statuses(filter func(*model.Status) bool, withMessage bool) []*model.Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*model.Status, 0, len(e.statuses))
	for _, st := range e.statuses {
		if filter != nil && !filter(st) {
			continue
		}
		c := *st
		if !withMessage {
			c.Message = ""
		}
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Test < out[j].Test
	})
	return out
}

// Drop removes a status (test != "") or every status of a host.
func (e *Engine) Drop(ctx context.Context, host, test string) error {
	e.mu.Lock()
	var removed []string
	for k, s := range e.statuses {
		if s.Host == host && (test == "" || s.Test == test) {
			delete(e.statuses, k)
			removed = append(removed, s.Test)
		}
	}
	e.mu.Unlock()
	for _, t := range removed {
		if err := e.store.DeleteStatus(ctx, host, t); err != nil {
			return err
		}
	}
	if test == "" {
		return e.DeleteHost(ctx, host)
	}
	e.publish(Event{Type: EventDropped, Host: host, Test: test, Time: time.Now()})
	return nil
}

// Disable disables matching tests (test may be "*" or a regex prefixed with %).
// dur < 0 disables until the status is OK again.
func (e *Engine) Disable(ctx context.Context, host, test string, dur time.Duration, msg string) (int, error) {
	match, err := testMatcher(test)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	until := now.Add(dur)
	if dur < 0 {
		until = untilOK
	}
	var changed []*model.Status
	e.mu.Lock()
	for _, s := range e.statuses {
		if s.Host == host && match(s.Test) {
			s.DisabledUntil = &until
			s.DisableMsg = msg
			if s.Color != protocol.Blue {
				s.Color = protocol.Blue
				s.LastChange = now
			}
			c := *s
			changed = append(changed, &c)
		}
	}
	e.mu.Unlock()
	for _, s := range changed {
		if err := e.store.SaveStatus(ctx, s); err != nil {
			return 0, err
		}
		e.publish(Event{Type: EventStatus, Host: s.Host, Test: s.Test, Color: s.Color, Changed: true, Status: s, Time: now})
	}
	return len(changed), nil
}

// Enable re-enables matching tests.
func (e *Engine) Enable(ctx context.Context, host, test string) (int, error) {
	match, err := testMatcher(test)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	var changed []*model.Status
	e.mu.Lock()
	for _, s := range e.statuses {
		if s.Host == host && match(s.Test) && s.DisabledUntil != nil {
			s.DisabledUntil, s.DisableMsg = nil, ""
			s.Color = s.RealColor
			if s.ValidUntil.Before(now) {
				s.Color = protocol.Purple
			}
			s.LastChange = now
			c := *s
			changed = append(changed, &c)
		}
	}
	e.mu.Unlock()
	for _, s := range changed {
		if err := e.store.SaveStatus(ctx, s); err != nil {
			return 0, err
		}
		e.publish(Event{Type: EventStatus, Host: s.Host, Test: s.Test, Color: s.Color, PrevColor: protocol.Blue, Changed: true, Status: s, Time: now})
	}
	return len(changed), nil
}

// Ack acknowledges an alerting status by cookie (cookie > 0) or host/test.
func (e *Engine) Ack(ctx context.Context, cookie int64, host, test string, dur time.Duration, msg, by string) (int, error) {
	now := time.Now()
	until := now.Add(dur)
	var acked []*model.Status
	e.mu.Lock()
	for _, s := range e.statuses {
		if !s.Color.IsAlert() && s.Color != protocol.Purple {
			continue
		}
		if (cookie > 0 && s.Cookie == cookie) || (cookie <= 0 && s.Host == host && (test == "" || s.Test == test)) {
			s.AckUntil, s.AckMsg, s.AckBy = &until, msg, by
			c := *s
			acked = append(acked, &c)
		}
	}
	e.mu.Unlock()
	for _, s := range acked {
		if err := e.store.SaveStatus(ctx, s); err != nil {
			return 0, err
		}
		e.publish(Event{Type: EventStatus, Host: s.Host, Test: s.Test, Color: s.Color, PrevColor: s.Color, Status: s, Time: now})
	}
	return len(acked), nil
}

func testMatcher(pattern string) (func(string) bool, error) {
	switch {
	case pattern == "" || pattern == "*":
		return func(string) bool { return true }, nil
	case strings.HasPrefix(pattern, "%"):
		re, err := regexp.Compile(pattern[1:])
		if err != nil {
			return nil, err
		}
		return re.MatchString, nil
	default:
		set := map[string]bool{}
		for _, p := range strings.Split(pattern, ",") {
			set[p] = true
		}
		return func(s string) bool { return set[s] }, nil
	}
}

// ---- background loops ----

func (e *Engine) sweepLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.sweep(ctx)
		}
	}
}

// sweep turns stale statuses purple and expires disables.
func (e *Engine) sweep(ctx context.Context) {
	now := time.Now()
	type change struct {
		st   model.Status
		prev protocol.Color
	}
	var changes []change
	e.mu.Lock()
	for _, s := range e.statuses {
		prev := s.Color
		if s.DisabledUntil != nil && !s.DisabledUntil.After(now) {
			s.DisabledUntil, s.DisableMsg = nil, ""
			s.Color = s.RealColor
		}
		if s.DisabledUntil == nil && s.ValidUntil.Before(now) && s.Color != protocol.Purple {
			s.Color = protocol.Purple
		}
		if s.Color != prev {
			s.LastChange = now
			if s.Color == protocol.Purple {
				s.Summary = "Status is stale: no update received since " + s.LastUpdate.Format(time.RFC1123)
			}
			changes = append(changes, change{*s, prev})
		}
	}
	e.mu.Unlock()
	for _, c := range changes {
		st := c.st
		if err := e.store.SaveStatus(ctx, &st); err != nil {
			e.log.Warn("save status", "err", err)
		}
		ev := &model.HistoryEvent{Host: st.Host, Test: st.Test, Time: now, Color: st.Color, PrevColor: c.prev, Summary: st.Summary}
		if err := e.store.AddHistory(ctx, ev); err != nil {
			e.log.Warn("history insert", "err", err)
		}
		e.publish(Event{Type: EventStatus, Host: st.Host, Test: st.Test, Color: st.Color, PrevColor: c.prev, Changed: true, Status: &st, Time: now})
	}
}

// RecordMetrics queues metric samples for batched insertion.
func (e *Engine) RecordMetrics(host, test string, ms []protocol.Metric, t time.Time) {
	for _, m := range ms {
		select {
		case e.metrics <- model.MetricSample{Host: host, Test: test, Name: m.Name, Time: t, Value: m.Value}:
		default:
			e.log.Warn("metrics buffer full, dropping samples")
			return
		}
	}
}

func (e *Engine) metricsLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	batch := make([]model.MetricSample, 0, 5000)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := e.store.InsertMetrics(fctx, batch); err != nil {
			e.log.Warn("insert metrics", "err", err, "count", len(batch))
		}
		cancel()
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			// drain
			for {
				select {
				case m := <-e.metrics:
					batch = append(batch, m)
				default:
					flush()
					return
				}
			}
		case m := <-e.metrics:
			batch = append(batch, m)
			if len(batch) >= 5000 {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}
